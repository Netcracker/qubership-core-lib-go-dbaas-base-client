package dbaasbase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model"
	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model/rest"
	"golang.org/x/sync/singleflight"
)

const (
	mountedSecretPath = "/etc/secrets/dbaas-secrets"

	metadataFileName             = "metadata.json"
	connectionPropertiesFileName = "connectionProperties.json"
	rescanThrottleDuration       = 60 * time.Second
)

type secretMetadata struct {
	Classifier map[string]interface{} `json:"classifier"`
	Type       string                 `json:"type"`
	UserRole   string                 `json:"userRole,omitempty"`
	Id         string                 `json:"id,omitempty"`
	Name       string                 `json:"name,omitempty"`
	Namespace  string                 `json:"namespace,omitempty"`
	Settings   map[string]interface{} `json:"settings,omitempty"`
}

type indexEntry struct {
	dirPath string
	meta    secretMetadata
}

type secretIndex struct {
	mu         sync.RWMutex
	index      map[string]indexEntry // matchingKey → entry
	basePath   string
	lastRescan time.Time
	sfGroup    singleflight.Group
}

func newSecretIndex(basePath string) *secretIndex {
	idx := &secretIndex{
		basePath: basePath,
		index:    make(map[string]indexEntry),
	}
	idx.buildIndex()
	return idx
}

func (idx *secretIndex) buildIndex() {
	entries, err := os.ReadDir(idx.basePath)
	if err != nil {
		logger.Warnf("mounted-secret: cannot read secret path %q: %v", idx.basePath, err)
		return
	}

	newIndex := make(map[string]indexEntry)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(idx.basePath, entry.Name())
		indexSecretDir(dirPath, newIndex)
	}

	idx.mu.Lock()
	idx.index = newIndex
	idx.lastRescan = time.Now()
	idx.mu.Unlock()
}

func indexSecretDir(dirPath string, newIndex map[string]indexEntry) {
	data, err := os.ReadFile(filepath.Join(dirPath, metadataFileName))
	if err != nil {
		// no metadata.json — skip silently
		return
	}

	var meta secretMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		logger.Warnf("mounted-secret: corrupt %s in %q: %v", metadataFileName, dirPath, err)
		return
	}

	if len(meta.Classifier) == 0 || meta.Type == "" {
		logger.Warnf("mounted-secret: incomplete metadata in %q (missing classifier or type), skipping", dirPath)
		return
	}

	key := matchingKey(meta.Classifier, meta.Type, meta.UserRole)
	if key == "" || strings.HasPrefix(key, "|") {
		logger.Warnf("mounted-secret: could not build canonical key for %q, skipping", dirPath)
		return
	}
	if existing, dup := newIndex[key]; dup {
		logger.Warnf("mounted-secret: duplicate key %q in %q and %q — second entry wins; check operator configuration", key, existing.dirPath, dirPath)
	}
	newIndex[key] = indexEntry{dirPath: dirPath, meta: meta}
}

// resolve looks up the index and reads connectionProperties.json fresh on every call.
// Returns (nil, nil, false) on miss so the caller falls through to REST.
func (idx *secretIndex) resolve(clf map[string]interface{}, dbType, role string) (map[string]interface{}, *secretMetadata, bool) {
	key := matchingKey(clf, dbType, role)

	idx.mu.RLock()
	entry, found := idx.index[key]
	sinceRescan := time.Since(idx.lastRescan)
	idx.mu.RUnlock()

	if !found {
		if sinceRescan >= rescanThrottleDuration {
			// singleflight collapses concurrent misses into one buildIndex call.
			idx.sfGroup.Do("rescan", func() (interface{}, error) {
				idx.buildIndex()
				return nil, nil
			})
		}
		idx.mu.RLock()
		entry, found = idx.index[key]
		idx.mu.RUnlock()
		if !found {
			return nil, nil, false
		}
	}

	propsPath := filepath.Join(entry.dirPath, connectionPropertiesFileName)
	data, err := os.ReadFile(propsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Secret directory was removed; evict the stale index entry so subsequent
			// calls don't keep hitting a path that no longer exists.
			idx.mu.Lock()
			delete(idx.index, key)
			idx.mu.Unlock()
			logger.Warnf("mounted-secret: secret removed from disk, evicting index entry for %q", entry.dirPath)
		} else {
			logger.Warnf("mounted-secret: cannot read %s in %q: %v", connectionPropertiesFileName, entry.dirPath, err)
		}
		return nil, nil, false
	}

	props := make(map[string]interface{})
	if err := json.Unmarshal(data, &props); err != nil {
		logger.Warnf("mounted-secret: corrupt %s in %q: %v", connectionPropertiesFileName, entry.dirPath, err)
		return nil, nil, false
	}

	return props, &entry.meta, true
}

// matchingKey builds the canonical lookup key: canonical(clf)|type|role
// Role matching is exact string equality (after TrimSpace) against metadata.userRole.
// There is no aggregator-side role resolution here — the client's params.Role and the
// operator's metadata.userRole must match as strings. An empty role matches a secret
// whose metadata.userRole was left unset (empty string).
func matchingKey(clf map[string]interface{}, dbType, role string) string {
	return canonicalClassifier(clf) + "|" + strings.ToLower(dbType) + "|" + strings.TrimSpace(role)
}

func canonicalClassifier(clf map[string]interface{}) string {
	b, err := marshalCanonical(clf)
	if err != nil {
		logger.Warnf("mounted-secret: failed to canonicalize classifier: %v", err)
		return ""
	}
	return string(b)
}

func marshalCanonical(m map[string]interface{}) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteByte('{')
	first := true
	for _, k := range keys {
		vBytes, err := marshalValue(k, m[k])
		if err != nil {
			return nil, err
		}
		if vBytes == nil {
			continue
		}
		if !first {
			sb.WriteByte(',')
		}
		kBytes, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		sb.Write(kBytes)
		sb.WriteByte(':')
		sb.Write(vBytes)
		first = false
	}
	sb.WriteByte('}')
	return []byte(sb.String()), nil
}

// marshalValue encodes a single classifier value canonically.
// Returns nil to signal that the entry should be omitted.
func marshalValue(key string, v interface{}) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case string:
		if key == "scope" {
			val = strings.ToLower(val)
		}
		if val == "" {
			return nil, nil
		}
		return json.Marshal(val)
	case map[string]interface{}:
		b, err := marshalCanonical(val)
		if err != nil || string(b) == "{}" {
			return nil, err
		}
		return b, nil
	default:
		return json.Marshal(val)
	}
}

type mountedSecretProvider struct {
	idx *secretIndex
}

func newMountedSecretProvider() *mountedSecretProvider {
	return newMountedSecretProviderForPath(mountedSecretPath)
}

func newMountedSecretProviderForPath(basePath string) *mountedSecretProvider {
	return &mountedSecretProvider{idx: newSecretIndex(basePath)}
}

func (p *mountedSecretProvider) GetOrCreateDb(dbType string, clf map[string]interface{}, params rest.BaseDbParams) (*model.LogicalDb, error) {
	props, meta, ok := p.idx.resolve(clf, dbType, params.Role)
	if !ok {
		return nil, nil
	}
	logger.Debugf("mounted-secret: GetOrCreateDb hit for type=%s classifier=%+v", dbType, clf)

	// Prefer fields from metadata (populated by operator >= v8d7552a).
	// Fall back to classifier["namespace"] / connectionProperties["name"] for older Secrets
	// that predate those metadata fields.
	ns := meta.Namespace
	if ns == "" {
		ns, _ = clf["namespace"].(string)
	}
	name := meta.Name
	if name == "" {
		name, _ = props["name"].(string)
	}

	return &model.LogicalDb{
		Id:                   meta.Id,
		Classifier:           clf,
		Type:                 dbType,
		ConnectionProperties: props,
		Namespace:            ns,
		Name:                 name,
		Settings:             meta.Settings,
	}, nil
}

func (p *mountedSecretProvider) GetConnection(dbType string, clf map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	props, _, ok := p.idx.resolve(clf, dbType, params.Role)
	if !ok {
		return nil, nil
	}
	logger.Debugf("mounted-secret: GetConnection hit for type=%s classifier=%+v role=%s", dbType, clf, params.Role)
	return props, nil
}

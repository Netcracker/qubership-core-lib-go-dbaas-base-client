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
)

const (
	MountedSecretEnabledKey  = "dbaas.connection-properties.mounted-secret.enabled"
	MountedSecretBasePathKey = "dbaas.connection-properties.mounted-secret.base-path"
	mountedSecretDefaultPath = "/var/run/dbaas"

	metadataFileName            = "metadata.json"
	connectionPropertiesFileName = "connectionProperties.json"
	rescanThrottleDuration      = 5 * time.Second
)

type secretMetadata struct {
	Classifier map[string]interface{} `json:"classifier"`
	Type       string                 `json:"type"`
	UserRole   string                 `json:"userRole,omitempty"`
}

type secretIndex struct {
	mu         sync.RWMutex
	index      map[string]string // matchingKey → directory path
	basePath   string
	lastRescan time.Time
}

func newSecretIndex(basePath string) *secretIndex {
	idx := &secretIndex{
		basePath: basePath,
		index:    make(map[string]string),
	}
	idx.buildIndex()
	return idx
}

func (idx *secretIndex) buildIndex() {
	entries, err := os.ReadDir(idx.basePath)
	if err != nil {
		logger.Warnf("mounted-secret: cannot read base-path %q: %v", idx.basePath, err)
		return
	}

	newIndex := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(idx.basePath, entry.Name())
		metaPath := filepath.Join(dirPath, metadataFileName)

		data, err := os.ReadFile(metaPath)
		if err != nil {
			// no metadata.json — skip silently
			continue
		}

		var meta secretMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			logger.Warnf("mounted-secret: corrupt %s in %q: %v", metadataFileName, dirPath, err)
			continue
		}

		key := matchingKey(meta.Classifier, meta.Type, meta.UserRole)
		newIndex[key] = dirPath
	}

	idx.mu.Lock()
	idx.index = newIndex
	idx.lastRescan = time.Now()
	idx.mu.Unlock()
}

// resolve looks up the index and reads connectionProperties.json fresh on every call.
// Returns (nil, false) on miss so the caller falls through to REST.
func (idx *secretIndex) resolve(clf map[string]interface{}, dbType, role string) (map[string]interface{}, bool) {
	key := matchingKey(clf, dbType, role)

	idx.mu.RLock()
	dirPath, found := idx.index[key]
	sinceRescan := time.Since(idx.lastRescan)
	idx.mu.RUnlock()

	if !found {
		if sinceRescan >= rescanThrottleDuration {
			idx.buildIndex()
			idx.mu.RLock()
			dirPath, found = idx.index[key]
			idx.mu.RUnlock()
		}

		if !found {
			return nil, false
		}
	}

	propsPath := filepath.Join(dirPath, connectionPropertiesFileName)
	data, err := os.ReadFile(propsPath)
	if err != nil {
		logger.Warnf("mounted-secret: cannot read %s in %q: %v", connectionPropertiesFileName, dirPath, err)
		return nil, false
	}

	props := make(map[string]interface{})
	if err := json.Unmarshal(data, &props); err != nil {
		logger.Warnf("mounted-secret: corrupt %s in %q: %v", connectionPropertiesFileName, dirPath, err)
		return nil, false
	}

	return props, true
}

// matchingKey builds the canonical lookup key: canonical(clf)|type|role
func matchingKey(clf map[string]interface{}, dbType, role string) string {
	return canonicalClassifier(clf) + "|" + strings.ToLower(dbType) + "|" + strings.TrimSpace(role)
}

func canonicalClassifier(clf map[string]interface{}) string {
	cleaned := canonicalMap(clf)
	b, err := marshalSorted(cleaned)
	if err != nil {
		logger.Warnf("mounted-secret: failed to canonicalize classifier: %v", err)
		return ""
	}
	return string(b)
}

func canonicalMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			s := val
			if k == "scope" {
				s = strings.ToLower(s)
			}
			if s == "" {
				continue
			}
			out[k] = s
		case map[string]interface{}:
			nested := canonicalMap(val)
			if len(nested) > 0 {
				out[k] = nested
			}
		default:
			out[k] = v
		}
	}
	return out
}

func marshalSorted(v interface{}) ([]byte, error) {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var sb strings.Builder
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			kBytes, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			vBytes, err := marshalSorted(val[k])
			if err != nil {
				return nil, err
			}
			sb.Write(kBytes)
			sb.WriteByte(':')
			sb.Write(vBytes)
		}
		sb.WriteByte('}')
		return []byte(sb.String()), nil
	default:
		return json.Marshal(v)
	}
}

type mountedSecretProvider struct {
	idx *secretIndex
}

func newMountedSecretProvider(basePath string) *mountedSecretProvider {
	return &mountedSecretProvider{idx: newSecretIndex(basePath)}
}

func (p *mountedSecretProvider) GetOrCreateDb(dbType string, clf map[string]interface{}, _ rest.BaseDbParams) (*model.LogicalDb, error) {
	props, ok := p.idx.resolve(clf, dbType, "")
	if !ok {
		return nil, nil
	}
	logger.Debugf("mounted-secret: GetOrCreateDb hit for type=%s classifier=%+v", dbType, clf)
	return &model.LogicalDb{
		Classifier:           clf,
		Type:                 dbType,
		ConnectionProperties: props,
	}, nil
}

func (p *mountedSecretProvider) GetConnection(dbType string, clf map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	props, ok := p.idx.resolve(clf, dbType, params.Role)
	if !ok {
		return nil, nil
	}
	logger.Debugf("mounted-secret: GetConnection hit for type=%s classifier=%+v role=%s", dbType, clf, params.Role)
	return props, nil
}

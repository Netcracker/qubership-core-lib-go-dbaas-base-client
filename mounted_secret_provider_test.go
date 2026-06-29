package dbaasbase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MountedSecretProviderTestSuite struct {
	suite.Suite
	baseDir string
}

func (s *MountedSecretProviderTestSuite) SetupTest() {
	s.baseDir = s.T().TempDir()
}

func TestMountedSecretProviderSuite(t *testing.T) {
	suite.Run(t, new(MountedSecretProviderTestSuite))
}

func (s *MountedSecretProviderTestSuite) writeSecret(secretName string, meta secretMetadata, props map[string]interface{}) string {
	dir := filepath.Join(s.baseDir, secretName)
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))

	metaBytes, err := json.Marshal(meta)
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, metadataFileName), metaBytes, 0o644))

	propsBytes, err := json.Marshal(props)
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, connectionPropertiesFileName), propsBytes, 0o644))

	return dir
}

func serviceClassifier(msName, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"microserviceName": msName,
		"namespace":        namespace,
		"scope":            "service",
	}
}

func tenantClassifier(msName, namespace, tenantID string) map[string]interface{} {
	return map[string]interface{}{
		"microserviceName": msName,
		"namespace":        namespace,
		"scope":            "tenant",
		"tenantId":         tenantID,
	}
}

func (s *MountedSecretProviderTestSuite) TestBuildIndex_ServiceScope() {
	clf := serviceClassifier("my-svc", "team-a")
	props := map[string]interface{}{"url": "postgres://host/db", "username": "u", "password": "p"}
	s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	idx := newSecretIndex(s.baseDir)
	resolved, _, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "postgres://host/db", resolved["url"])
}

func (s *MountedSecretProviderTestSuite) TestBuildIndex_TenantScope() {
	clf := tenantClassifier("my-svc", "team-a", "acme")
	props := map[string]interface{}{"url": "mongo://host/db", "username": "u", "password": "p"}
	s.writeSecret("secret-tenant", secretMetadata{Classifier: clf, Type: "mongodb"}, props)

	idx := newSecretIndex(s.baseDir)
	resolved, _, ok := idx.resolve(clf, "mongodb", "")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "mongo://host/db", resolved["url"])
}

func (s *MountedSecretProviderTestSuite) TestResolve_Miss_UnknownClassifier() {
	clf := serviceClassifier("my-svc", "team-a")
	props := map[string]interface{}{"url": "postgres://host/db"}
	s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	idx := newSecretIndex(s.baseDir)
	resolved, _, ok := idx.resolve(serviceClassifier("other-svc", "team-a"), "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestResolve_Miss_EmptyDir() {
	idx := newSecretIndex(s.baseDir)
	resolved, _, ok := idx.resolve(serviceClassifier("svc", "ns"), "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestResolve_Miss_BasepathMissing() {
	idx := newSecretIndex("/nonexistent/path")
	resolved, _, ok := idx.resolve(serviceClassifier("svc", "ns"), "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestBuildIndex_CorruptMetadata_Skipped() {
	dir := filepath.Join(s.baseDir, "bad-secret")
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, metadataFileName), []byte("not json{{"), 0o644))

	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host"}
	s.writeSecret("good-secret", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	idx := newSecretIndex(s.baseDir)
	_, _, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok, "good secret should still resolve despite corrupt neighbour")
}

func (s *MountedSecretProviderTestSuite) TestResolve_CorruptConnectionProperties() {
	clf := serviceClassifier("svc", "ns")
	dir := filepath.Join(s.baseDir, "corrupt-props")
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))

	metaBytes, _ := json.Marshal(secretMetadata{Classifier: clf, Type: "postgresql"})
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, metadataFileName), metaBytes, 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, connectionPropertiesFileName), []byte("not json{{"), 0o644))

	idx := newSecretIndex(s.baseDir)
	resolved, _, ok := idx.resolve(clf, "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestGetConnection_ReadsFileFresh() {
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host", "password": "old-pass"}
	dir := s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p := newMountedSecretProviderForPath(s.baseDir)

	conn1, err := p.GetConnection("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "old-pass", conn1["password"])

	newBytes, _ := json.Marshal(map[string]interface{}{"url": "pg://host", "password": "new-pass"})
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, connectionPropertiesFileName), newBytes, 0o644))

	conn2, err := p.GetConnection("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	assert.Equal(s.T(), "new-pass", conn2["password"], "provider must re-read the file, not return cached props")
}

func (s *MountedSecretProviderTestSuite) TestGetOrCreateDb_ReturnsLogicalDb() {
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host", "username": "u"}
	meta := secretMetadata{
		Classifier: clf,
		Type:       "postgresql",
		Id:         "db-123",
		Name:       "my-db",
		Namespace:  "ns",
		Settings:   map[string]interface{}{"poolSize": float64(5)},
	}
	s.writeSecret("secret-a", meta, props)

	p := newMountedSecretProviderForPath(s.baseDir)
	db, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), db)
	assert.Equal(s.T(), "postgresql", db.Type)
	assert.Equal(s.T(), clf, db.Classifier)
	assert.Equal(s.T(), "pg://host", db.ConnectionProperties["url"])
	assert.Equal(s.T(), "db-123", db.Id)
	assert.Equal(s.T(), "my-db", db.Name)
	assert.Equal(s.T(), "ns", db.Namespace)
	assert.Equal(s.T(), map[string]interface{}{"poolSize": float64(5)}, db.Settings)
}

func (s *MountedSecretProviderTestSuite) TestGetOrCreateDb_ReturnsDefensiveMapCopies() {
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host", "username": "u"}
	meta := secretMetadata{
		Classifier: clf,
		Type:       "postgresql",
		Id:         "db-123",
		Name:       "my-db",
		Namespace:  "ns",
		Settings:   map[string]interface{}{"poolSize": float64(5)},
	}
	s.writeSecret("secret-a", meta, props)

	p := newMountedSecretProviderForPath(s.baseDir)
	db, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), db)

	db.Classifier["namespace"] = "mutated"
	db.ConnectionProperties["url"] = "pg://mutated"
	db.Settings["poolSize"] = float64(99)
	assert.Equal(s.T(), "ns", clf["namespace"], "returned classifier must not share the caller map")

	dbAgain, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), dbAgain)
	assert.Equal(s.T(), "ns", dbAgain.Classifier["namespace"])
	assert.Equal(s.T(), "pg://host", dbAgain.ConnectionProperties["url"])
	assert.Equal(s.T(), float64(5), dbAgain.Settings["poolSize"])
}

// Backward-compat: older Secrets written before the operator added id/name/namespace/settings
// to metadata.json. Namespace must fall back to classifier["namespace"], Name to
// connectionProperties["name"], and Id/Settings remain empty.
func (s *MountedSecretProviderTestSuite) TestGetOrCreateDb_BackwardCompat_OldMetadata() {
	clf := serviceClassifier("svc", "team-b")
	props := map[string]interface{}{"url": "pg://host", "name": "legacy-db"}
	s.writeSecret("secret-old", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p := newMountedSecretProviderForPath(s.baseDir)
	db, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), db)
	assert.Equal(s.T(), "team-b", db.Namespace, "Namespace must fall back to classifier[namespace]")
	assert.Equal(s.T(), "legacy-db", db.Name, "Name must fall back to connectionProperties[name]")
	assert.Empty(s.T(), db.Id)
	assert.Nil(s.T(), db.Settings)
}
func (s *MountedSecretProviderTestSuite) TestGetOrCreateDb_Miss_ReturnsNil() {
	p := newMountedSecretProviderForPath(s.baseDir)
	db, err := p.GetOrCreateDb("postgresql", serviceClassifier("svc", "ns"), rest.BaseDbParams{})
	assert.NoError(s.T(), err)
	assert.Nil(s.T(), db)
}

func (s *MountedSecretProviderTestSuite) TestGetConnection_Miss_ReturnsNil() {
	p := newMountedSecretProviderForPath(s.baseDir)
	props, err := p.GetConnection("postgresql", serviceClassifier("svc", "ns"), rest.BaseDbParams{})
	assert.NoError(s.T(), err)
	assert.Nil(s.T(), props)
}

func (s *MountedSecretProviderTestSuite) TestResolve_WithRole() {
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://ro-host"}
	s.writeSecret("secret-ro", secretMetadata{Classifier: clf, Type: "postgresql", UserRole: "ro"}, props)

	idx := newSecretIndex(s.baseDir)

	resolved, _, ok := idx.resolve(clf, "postgresql", "ro")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "pg://ro-host", resolved["url"])

	_, _, ok = idx.resolve(clf, "postgresql", "admin")
	assert.False(s.T(), ok)
}

// GetOrCreateDb must forward params.Role into resolve (symmetric with GetConnection).
// An explicit role hits only the matching secret; an empty role hits a roleless secret.
func (s *MountedSecretProviderTestSuite) TestGetOrCreateDb_ForwardsRole() {
	clf := serviceClassifier("svc", "ns")
	s.writeSecret("secret-ro", secretMetadata{Classifier: clf, Type: "postgresql", UserRole: "ro"},
		map[string]interface{}{"url": "pg://ro-host"})
	s.writeSecret("secret-admin", secretMetadata{Classifier: clf, Type: "postgresql", UserRole: "admin"},
		map[string]interface{}{"url": "pg://admin-host"})

	p := newMountedSecretProviderForPath(s.baseDir)

	dbRo, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{Role: "ro"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), dbRo)
	assert.Equal(s.T(), "pg://ro-host", dbRo.ConnectionProperties["url"])

	dbAdmin, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{Role: "admin"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), dbAdmin)
	assert.Equal(s.T(), "pg://admin-host", dbAdmin.ConnectionProperties["url"])

	// empty role does not match a secret with an explicit userRole
	dbNone, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	assert.Nil(s.T(), dbNone, "empty role must not match a secret that has userRole set")
}

func (s *MountedSecretProviderTestSuite) TestRescan_NewSecretPickedUp() {
	clf := serviceClassifier("new-svc", "ns")

	p := newMountedSecretProviderForPath(s.baseDir)
	db, _ := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	assert.Nil(s.T(), db)

	props := map[string]interface{}{"url": "pg://new-host"}
	s.writeSecret("new-secret", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p.idx.mu.Lock()
	p.idx.lastRescan = time.Now().Add(-rescanThrottleDuration - time.Second)
	p.idx.mu.Unlock()

	db, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), db, "provider should pick up the new secret after throttled rescan")
	assert.Equal(s.T(), "pg://new-host", db.ConnectionProperties["url"])
}

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_ScopeNormalized() {
	clf1 := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "Service"}
	clf2 := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "service"}
	assert.Equal(s.T(), canonicalClassifier(clf1), canonicalClassifier(clf2))
}

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_StableKeyOrder() {
	a := map[string]interface{}{"namespace": "ns", "microserviceName": "svc", "scope": "service"}
	b := map[string]interface{}{"microserviceName": "svc", "scope": "service", "namespace": "ns"}
	assert.Equal(s.T(), canonicalClassifier(a), canonicalClassifier(b))
}

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_OmitsEmptyValues() {
	with := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "service", "tenantId": ""}
	without := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "service"}
	assert.Equal(s.T(), canonicalClassifier(with), canonicalClassifier(without))
}

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_NestedCustomKeys() {
	clf := map[string]interface{}{
		"microserviceName": "svc",
		"namespace":        "ns",
		"scope":            "service",
		"customKeys":       map[string]interface{}{"z": "1", "a": "2"},
	}
	key := canonicalClassifier(clf)
	assert.True(s.T(), strings.Index(key, `"a"`) < strings.Index(key, `"z"`), "customKeys must be sorted: got %s", key)
}

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_PreservesEmptyCustomKeyValues() {
	withEmptyCustomKey := map[string]interface{}{
		"microserviceName": "svc",
		"namespace":        "ns",
		"scope":            "service",
		"customKeys":       map[string]interface{}{"logicalDBName": ""},
	}
	withoutCustomKeys := map[string]interface{}{
		"microserviceName": "svc",
		"namespace":        "ns",
		"scope":            "service",
	}

	assert.NotEqual(s.T(), canonicalClassifier(withEmptyCustomKey), canonicalClassifier(withoutCustomKeys))
	assert.Contains(s.T(), canonicalClassifier(withEmptyCustomKey), `"logicalDBName":""`)
}

func (s *MountedSecretProviderTestSuite) TestResolve_EmptyCustomKeyDoesNotMatchMissingCustomKeys() {
	withEmptyCustomKey := map[string]interface{}{
		"microserviceName": "svc",
		"namespace":        "ns",
		"scope":            "service",
		"customKeys":       map[string]interface{}{"logicalDBName": ""},
	}
	withoutCustomKeys := map[string]interface{}{
		"microserviceName": "svc",
		"namespace":        "ns",
		"scope":            "service",
	}
	s.writeSecret("secret-empty-custom-key", secretMetadata{Classifier: withEmptyCustomKey, Type: "postgresql"},
		map[string]interface{}{"url": "pg://empty-custom-key"})

	idx := newSecretIndex(s.baseDir)
	resolved, _, ok := idx.resolve(withEmptyCustomKey, "postgresql", "")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "pg://empty-custom-key", resolved["url"])

	resolved, _, ok = idx.resolve(withoutCustomKeys, "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestMatchingKey_TypeLowercased() {
	clf := serviceClassifier("svc", "ns")
	assert.Equal(s.T(), matchingKey(clf, "PostgreSQL", ""), matchingKey(clf, "postgresql", ""))
}

func (s *MountedSecretProviderTestSuite) TestBuildIndex_DuplicateKey_LowestDirNameWins() {
	clf := serviceClassifier("svc", "ns")
	props1 := map[string]interface{}{"url": "pg://first"}
	props2 := map[string]interface{}{"url": "pg://second"}
	// "secret-first" < "secret-second": the lowest directory name must win, deterministically.
	s.writeSecret("secret-first", secretMetadata{Classifier: clf, Type: "postgresql"}, props1)
	s.writeSecret("secret-second", secretMetadata{Classifier: clf, Type: "postgresql"}, props2)

	idx := newSecretIndex(s.baseDir)
	resolved, _, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "pg://first", resolved["url"], "lowest directory name must win on a duplicate key")
}

func (s *MountedSecretProviderTestSuite) TestResolve_ReadsMetadataFresh() {
	clf := serviceClassifier("svc", "ns")
	dir := s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql", Name: "n1"},
		map[string]interface{}{"url": "pg://host"})

	idx := newSecretIndex(s.baseDir)
	_, meta, ok := idx.resolve(clf, "postgresql", "")
	require.True(s.T(), ok)
	assert.Equal(s.T(), "n1", meta.Name)

	// change a non-key descriptor field on disk — the next resolve must reflect it (fresh metadata).
	newMeta, _ := json.Marshal(secretMetadata{Classifier: clf, Type: "postgresql", Name: "n2"})
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, metadataFileName), newMeta, 0o644))

	_, meta, ok = idx.resolve(clf, "postgresql", "")
	require.True(s.T(), ok)
	assert.Equal(s.T(), "n2", meta.Name, "metadata must be re-read fresh on every resolve, not cached")
}

func (s *MountedSecretProviderTestSuite) TestResolve_MetadataChangedInPlace_Evicted() {
	clf := serviceClassifier("svc", "ns")
	dir := s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"},
		map[string]interface{}{"url": "pg://host"})

	idx := newSecretIndex(s.baseDir)
	_, _, ok := idx.resolve(clf, "postgresql", "")
	require.True(s.T(), ok)

	// the descriptor's classifier changes in place (different microserviceName -> different key)
	movedMeta, _ := json.Marshal(secretMetadata{Classifier: serviceClassifier("other", "ns"), Type: "postgresql"})
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, metadataFileName), movedMeta, 0o644))

	_, _, ok = idx.resolve(clf, "postgresql", "")
	assert.False(s.T(), ok, "a descriptor that changed classifier in place must be evicted, not served under the old key")

	idx.mu.RLock()
	_, still := idx.index[matchingKey(clf, "postgresql", "")]
	idx.mu.RUnlock()
	assert.False(s.T(), still, "stale entry should be evicted from the index")
}

func (s *MountedSecretProviderTestSuite) TestResolve_EvictsStaleEntry() {
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host"}
	dir := s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p := newMountedSecretProviderForPath(s.baseDir)

	// confirm it resolves before removal
	result, _, ok := p.idx.resolve(clf, "postgresql", "")
	require.True(s.T(), ok)
	assert.Equal(s.T(), "pg://host", result["url"])

	// remove the secret directory from disk
	require.NoError(s.T(), os.RemoveAll(dir))

	// next resolve should return nil and evict the entry
	result, _, ok = p.idx.resolve(clf, "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), result)

	// entry must be gone from the index
	p.idx.mu.RLock()
	_, still := p.idx.index[matchingKey(clf, "postgresql", "")]
	p.idx.mu.RUnlock()
	assert.False(s.T(), still, "stale entry should have been evicted from the index")
}

// writeRawSecret writes metadata.json as a pre-formed JSON literal (exactly as the
// dbaas-operator would emit it) so golden-contract tests are not affected by Go's
// json.Marshal key ordering.
func (s *MountedSecretProviderTestSuite) writeRawSecret(secretName, rawMeta string, props map[string]interface{}) {
	dir := filepath.Join(s.baseDir, secretName)
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, metadataFileName), []byte(rawMeta), 0o644))
	propsBytes, err := json.Marshal(props)
	require.NoError(s.T(), err)
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, connectionPropertiesFileName), propsBytes, 0o644))
}

// Golden-contract tests pin the cross-system canonicalization agreement: metadata.json is
// written as a raw literal (as the dbaas-operator would write it) and resolve must hit for
// the matching app-side classifier. Any drift between the operator's ClassifierFlatMap and
// this client's marshalCanonical surfaces here as a failing test rather than a silent miss.

func (s *MountedSecretProviderTestSuite) TestGoldenContract_ServiceScope() {
	rawMeta := `{"classifier":{"microserviceName":"my-svc","namespace":"team-a","scope":"service"},"type":"postgresql"}`
	s.writeRawSecret("golden-service", rawMeta, map[string]interface{}{"url": "pg://golden-service"})

	idx := newSecretIndex(s.baseDir)
	clf := map[string]interface{}{"microserviceName": "my-svc", "namespace": "team-a", "scope": "service"}
	resolved, _, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok, "service-scope golden contract: resolve must hit")
	assert.Equal(s.T(), "pg://golden-service", resolved["url"])
}

func (s *MountedSecretProviderTestSuite) TestGoldenContract_TenantScope() {
	rawMeta := `{"classifier":{"microserviceName":"my-svc","namespace":"team-a","scope":"tenant","tenantId":"acme"},"type":"mongodb"}`
	s.writeRawSecret("golden-tenant", rawMeta, map[string]interface{}{"url": "mongo://golden-tenant"})

	idx := newSecretIndex(s.baseDir)
	clf := map[string]interface{}{"microserviceName": "my-svc", "namespace": "team-a", "scope": "tenant", "tenantId": "acme"}
	resolved, _, ok := idx.resolve(clf, "mongodb", "")
	assert.True(s.T(), ok, "tenant-scope golden contract: resolve must hit")
	assert.Equal(s.T(), "mongo://golden-tenant", resolved["url"])
}

func (s *MountedSecretProviderTestSuite) TestGoldenContract_CustomKeys_Nested() {
	// Operator stores customKeys as a nested map. App must also pass customKeys as a nested
	// map — passing them flat (as top-level keys) is a different classifier and will miss.
	rawMeta := `{"classifier":{"customKeys":{"env":"prod","tier":"db"},"microserviceName":"my-svc","namespace":"team-a","scope":"service"},"type":"postgresql"}`
	s.writeRawSecret("golden-custom", rawMeta, map[string]interface{}{"url": "pg://golden-custom"})

	idx := newSecretIndex(s.baseDir)
	clf := map[string]interface{}{
		"microserviceName": "my-svc",
		"namespace":        "team-a",
		"scope":            "service",
		"customKeys":       map[string]interface{}{"env": "prod", "tier": "db"},
	}
	resolved, _, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok, "customKeys golden contract: resolve must hit when app passes customKeys as nested map")
	assert.Equal(s.T(), "pg://golden-custom", resolved["url"])
}

// ── negative matching: tenant / role / extra-key (the "tricky" misses) ──────

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_ServiceVsTenantDiffer() {
	assert.NotEqual(s.T(), canonicalClassifier(serviceClassifier("svc", "ns")),
		canonicalClassifier(tenantClassifier("svc", "ns", "acme")),
		"service and tenant scope are different identities")
}

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_DifferentTenantIdDiffer() {
	assert.NotEqual(s.T(), canonicalClassifier(tenantClassifier("svc", "ns", "acme")),
		canonicalClassifier(tenantClassifier("svc", "ns", "globex")),
		"different tenantId is a different identity")
}

func (s *MountedSecretProviderTestSuite) TestCanonicalClassifier_TopLevelKeySetDiffers() {
	withExtra := serviceClassifier("svc", "ns")
	withExtra["logicalDbName"] = "reports"
	assert.NotEqual(s.T(), canonicalClassifier(serviceClassifier("svc", "ns")), canonicalClassifier(withExtra),
		"an arbitrary top-level identity key present on only one side must diverge the canonical key")
}

func (s *MountedSecretProviderTestSuite) TestMatchingKey_RoleCaseSensitive() {
	clf := serviceClassifier("svc", "ns")
	assert.NotEqual(s.T(), matchingKey(clf, "postgresql", "admin"), matchingKey(clf, "postgresql", "Admin"),
		"role is matched case-sensitively (unlike type, which is lower-cased)")
}

func (s *MountedSecretProviderTestSuite) TestResolve_ServiceSecretDoesNotMatchTenantRequest() {
	clf := serviceClassifier("svc", "ns")
	s.writeSecret("svc-secret", secretMetadata{Classifier: clf, Type: "postgresql"},
		map[string]interface{}{"url": "pg://svc"})

	idx := newSecretIndex(s.baseDir)
	_, _, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok, "service request hits the service secret")
	_, _, ok = idx.resolve(tenantClassifier("svc", "ns", "acme"), "postgresql", "")
	assert.False(s.T(), ok, "a tenant request must not be served by a service-scope secret")
}

func (s *MountedSecretProviderTestSuite) TestResolve_DifferentTenantId_Misses() {
	clf := tenantClassifier("svc", "ns", "acme")
	s.writeSecret("tenant-secret", secretMetadata{Classifier: clf, Type: "postgresql"},
		map[string]interface{}{"url": "pg://acme"})

	idx := newSecretIndex(s.baseDir)
	_, _, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok, "same tenantId hits")
	_, _, ok = idx.resolve(tenantClassifier("svc", "ns", "globex"), "postgresql", "")
	assert.False(s.T(), ok, "a different tenantId misses")
}

func (s *MountedSecretProviderTestSuite) TestResolve_RoleMatchingIsCaseSensitive() {
	clf := serviceClassifier("svc", "ns")
	s.writeSecret("admin-secret", secretMetadata{Classifier: clf, Type: "postgresql", UserRole: "admin"},
		map[string]interface{}{"url": "pg://admin"})

	idx := newSecretIndex(s.baseDir)
	_, _, ok := idx.resolve(clf, "postgresql", "admin")
	assert.True(s.T(), ok, "exact-case role hits")
	_, _, ok = idx.resolve(clf, "postgresql", "Admin")
	assert.False(s.T(), ok, "role is case-sensitive (unlike type): 'Admin' must not match 'admin'")
}

func (s *MountedSecretProviderTestSuite) TestResolve_ExtraTopLevelKeyOnOneSide_Misses() {
	withExtra := serviceClassifier("svc", "ns")
	withExtra["logicalDbName"] = "reports"
	s.writeSecret("extra-secret", secretMetadata{Classifier: withExtra, Type: "postgresql"},
		map[string]interface{}{"url": "pg://extra"})

	idx := newSecretIndex(s.baseDir)
	_, _, ok := idx.resolve(serviceClassifier("svc", "ns"), "postgresql", "")
	assert.False(s.T(), ok, "an extra top-level identity key on only the descriptor side must diverge the canonical key (silent-miss guard)")
	resolved, _, ok := idx.resolve(withExtra, "postgresql", "")
	assert.True(s.T(), ok, "the same extra key on both sides hits")
	assert.Equal(s.T(), "pg://extra", resolved["url"])
}

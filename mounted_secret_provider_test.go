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
	resolved, ok := idx.resolve(clf, "postgresql", "")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "postgres://host/db", resolved["url"])
}

func (s *MountedSecretProviderTestSuite) TestBuildIndex_TenantScope() {
	clf := tenantClassifier("my-svc", "team-a", "acme")
	props := map[string]interface{}{"url": "mongo://host/db", "username": "u", "password": "p"}
	s.writeSecret("secret-tenant", secretMetadata{Classifier: clf, Type: "mongodb"}, props)

	idx := newSecretIndex(s.baseDir)
	resolved, ok := idx.resolve(clf, "mongodb", "")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "mongo://host/db", resolved["url"])
}

func (s *MountedSecretProviderTestSuite) TestResolve_Miss_UnknownClassifier() {
	clf := serviceClassifier("my-svc", "team-a")
	props := map[string]interface{}{"url": "postgres://host/db"}
	s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	idx := newSecretIndex(s.baseDir)
	resolved, ok := idx.resolve(serviceClassifier("other-svc", "team-a"), "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestResolve_Miss_EmptyDir() {
	idx := newSecretIndex(s.baseDir)
	resolved, ok := idx.resolve(serviceClassifier("svc", "ns"), "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestResolve_Miss_BasepathMissing() {
	idx := newSecretIndex("/nonexistent/path")
	resolved, ok := idx.resolve(serviceClassifier("svc", "ns"), "postgresql", "")
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
	_, ok := idx.resolve(clf, "postgresql", "")
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
	resolved, ok := idx.resolve(clf, "postgresql", "")
	assert.False(s.T(), ok)
	assert.Nil(s.T(), resolved)
}

func (s *MountedSecretProviderTestSuite) TestGetConnection_ReadsFileFresh() {
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host", "password": "old-pass"}
	dir := s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p := newMountedSecretProvider(s.baseDir)

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
	s.writeSecret("secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p := newMountedSecretProvider(s.baseDir)
	db, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), db)
	assert.Equal(s.T(), "postgresql", db.Type)
	assert.Equal(s.T(), clf, db.Classifier)
	assert.Equal(s.T(), "pg://host", db.ConnectionProperties["url"])
}

func (s *MountedSecretProviderTestSuite) TestGetOrCreateDb_Miss_ReturnsNil() {
	p := newMountedSecretProvider(s.baseDir)
	db, err := p.GetOrCreateDb("postgresql", serviceClassifier("svc", "ns"), rest.BaseDbParams{})
	assert.NoError(s.T(), err)
	assert.Nil(s.T(), db)
}

func (s *MountedSecretProviderTestSuite) TestGetConnection_Miss_ReturnsNil() {
	p := newMountedSecretProvider(s.baseDir)
	props, err := p.GetConnection("postgresql", serviceClassifier("svc", "ns"), rest.BaseDbParams{})
	assert.NoError(s.T(), err)
	assert.Nil(s.T(), props)
}

func (s *MountedSecretProviderTestSuite) TestResolve_WithRole() {
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://ro-host"}
	s.writeSecret("secret-ro", secretMetadata{Classifier: clf, Type: "postgresql", UserRole: "ro"}, props)

	idx := newSecretIndex(s.baseDir)

	resolved, ok := idx.resolve(clf, "postgresql", "ro")
	assert.True(s.T(), ok)
	assert.Equal(s.T(), "pg://ro-host", resolved["url"])

	_, ok = idx.resolve(clf, "postgresql", "admin")
	assert.False(s.T(), ok)
}

func (s *MountedSecretProviderTestSuite) TestRescan_NewSecretPickedUp() {
	clf := serviceClassifier("new-svc", "ns")

	p := newMountedSecretProvider(s.baseDir)
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

func (s *MountedSecretProviderTestSuite) TestMatchingKey_TypeLowercased() {
	clf := serviceClassifier("svc", "ns")
	assert.Equal(s.T(), matchingKey(clf, "PostgreSQL", ""), matchingKey(clf, "postgresql", ""))
}

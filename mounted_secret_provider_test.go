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
)

// --- helpers ---

func writeSecret(t *testing.T, baseDir, secretName string, meta secretMetadata, props map[string]interface{}) string {
	t.Helper()
	dir := filepath.Join(baseDir, secretName)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	metaBytes, err := json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, metadataFileName), metaBytes, 0o644))

	propsBytes, err := json.Marshal(props)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, connectionPropertiesFileName), propsBytes, 0o644))

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

// --- index / canonical tests ---

func TestBuildIndex_ServiceScope(t *testing.T) {
	base := t.TempDir()
	clf := serviceClassifier("my-svc", "team-a")
	props := map[string]interface{}{"url": "postgres://host/db", "username": "u", "password": "p"}
	writeSecret(t, base, "secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	idx := newSecretIndex(base)
	resolved, ok := idx.resolve(clf, "postgresql", "")
	assert.True(t, ok)
	assert.Equal(t, "postgres://host/db", resolved["url"])
}

func TestBuildIndex_TenantScope(t *testing.T) {
	base := t.TempDir()
	clf := tenantClassifier("my-svc", "team-a", "acme")
	props := map[string]interface{}{"url": "mongo://host/db", "username": "u", "password": "p"}
	writeSecret(t, base, "secret-tenant", secretMetadata{Classifier: clf, Type: "mongodb"}, props)

	idx := newSecretIndex(base)
	resolved, ok := idx.resolve(clf, "mongodb", "")
	assert.True(t, ok)
	assert.Equal(t, "mongo://host/db", resolved["url"])
}

func TestResolve_Miss_UnknownClassifier(t *testing.T) {
	base := t.TempDir()
	clf := serviceClassifier("my-svc", "team-a")
	props := map[string]interface{}{"url": "postgres://host/db"}
	writeSecret(t, base, "secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	idx := newSecretIndex(base)
	otherClf := serviceClassifier("other-svc", "team-a")
	resolved, ok := idx.resolve(otherClf, "postgresql", "")
	assert.False(t, ok)
	assert.Nil(t, resolved)
}

func TestResolve_Miss_EmptyDir(t *testing.T) {
	base := t.TempDir()
	idx := newSecretIndex(base)
	resolved, ok := idx.resolve(serviceClassifier("svc", "ns"), "postgresql", "")
	assert.False(t, ok)
	assert.Nil(t, resolved)
}

func TestResolve_Miss_BasepathMissing(t *testing.T) {
	idx := newSecretIndex("/nonexistent/path")
	resolved, ok := idx.resolve(serviceClassifier("svc", "ns"), "postgresql", "")
	assert.False(t, ok)
	assert.Nil(t, resolved)
}

func TestBuildIndex_CorruptMetadata_Skipped(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "bad-secret")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, metadataFileName), []byte("not json{{"), 0o644))

	// good secret alongside the bad one
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host"}
	writeSecret(t, base, "good-secret", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	idx := newSecretIndex(base)
	_, ok := idx.resolve(clf, "postgresql", "")
	assert.True(t, ok, "good secret should still resolve despite corrupt neighbour")
}

func TestResolve_CorruptConnectionProperties(t *testing.T) {
	base := t.TempDir()
	clf := serviceClassifier("svc", "ns")
	dir := filepath.Join(base, "corrupt-props")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	metaBytes, _ := json.Marshal(secretMetadata{Classifier: clf, Type: "postgresql"})
	require.NoError(t, os.WriteFile(filepath.Join(dir, metadataFileName), metaBytes, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, connectionPropertiesFileName), []byte("not json{{"), 0o644))

	idx := newSecretIndex(base)
	resolved, ok := idx.resolve(clf, "postgresql", "")
	assert.False(t, ok)
	assert.Nil(t, resolved)
}

// --- rotation: re-reads file on each call ---

func TestGetConnection_ReadsFileFresh(t *testing.T) {
	base := t.TempDir()
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host", "password": "old-pass"}
	dir := writeSecret(t, base, "secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p := newMountedSecretProvider(base)

	conn1, err := p.GetConnection("postgresql", clf, rest.BaseDbParams{})
	require.NoError(t, err)
	assert.Equal(t, "old-pass", conn1["password"])

	// rotate the password in-place (kubelet updates the file)
	newProps := map[string]interface{}{"url": "pg://host", "password": "new-pass"}
	newBytes, _ := json.Marshal(newProps)
	require.NoError(t, os.WriteFile(filepath.Join(dir, connectionPropertiesFileName), newBytes, 0o644))

	conn2, err := p.GetConnection("postgresql", clf, rest.BaseDbParams{})
	require.NoError(t, err)
	assert.Equal(t, "new-pass", conn2["password"], "provider must re-read the file, not return cached props")
}

// --- GetOrCreateDb ---

func TestGetOrCreateDb_ReturnsLogicalDb(t *testing.T) {
	base := t.TempDir()
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://host", "username": "u"}
	writeSecret(t, base, "secret-a", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	p := newMountedSecretProvider(base)
	db, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(t, err)
	require.NotNil(t, db)
	assert.Equal(t, "postgresql", db.Type)
	assert.Equal(t, clf, db.Classifier)
	assert.Equal(t, "pg://host", db.ConnectionProperties["url"])
}

func TestGetOrCreateDb_Miss_ReturnsNil(t *testing.T) {
	base := t.TempDir()
	p := newMountedSecretProvider(base)
	db, err := p.GetOrCreateDb("postgresql", serviceClassifier("svc", "ns"), rest.BaseDbParams{})
	assert.NoError(t, err)
	assert.Nil(t, db)
}

func TestGetConnection_Miss_ReturnsNil(t *testing.T) {
	base := t.TempDir()
	p := newMountedSecretProvider(base)
	props, err := p.GetConnection("postgresql", serviceClassifier("svc", "ns"), rest.BaseDbParams{})
	assert.NoError(t, err)
	assert.Nil(t, props)
}

// --- role matching ---

func TestResolve_WithRole(t *testing.T) {
	base := t.TempDir()
	clf := serviceClassifier("svc", "ns")
	props := map[string]interface{}{"url": "pg://ro-host"}
	writeSecret(t, base, "secret-ro", secretMetadata{Classifier: clf, Type: "postgresql", UserRole: "ro"}, props)

	idx := newSecretIndex(base)

	// matching role hits
	resolved, ok := idx.resolve(clf, "postgresql", "ro")
	assert.True(t, ok)
	assert.Equal(t, "pg://ro-host", resolved["url"])

	// different role misses
	_, ok = idx.resolve(clf, "postgresql", "admin")
	assert.False(t, ok)
}

// --- throttled rescan ---

func TestRescan_NewSecretPickedUp(t *testing.T) {
	base := t.TempDir()
	clf := serviceClassifier("new-svc", "ns")

	p := newMountedSecretProvider(base)

	// miss before the secret exists
	db, _ := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	assert.Nil(t, db)

	// write the secret
	props := map[string]interface{}{"url": "pg://new-host"}
	writeSecret(t, base, "new-secret", secretMetadata{Classifier: clf, Type: "postgresql"}, props)

	// artificially age the lastRescan so the throttle passes
	p.idx.mu.Lock()
	p.idx.lastRescan = time.Now().Add(-rescanThrottleDuration - time.Second)
	p.idx.mu.Unlock()

	db, err := p.GetOrCreateDb("postgresql", clf, rest.BaseDbParams{})
	require.NoError(t, err)
	require.NotNil(t, db, "provider should pick up the new secret after throttled rescan")
	assert.Equal(t, "pg://new-host", db.ConnectionProperties["url"])
}

// --- canonical classifier ---

func TestCanonicalClassifier_ScopeNormalized(t *testing.T) {
	clf1 := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "Service"}
	clf2 := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "service"}
	assert.Equal(t, canonicalClassifier(clf1), canonicalClassifier(clf2))
}

func TestCanonicalClassifier_StableKeyOrder(t *testing.T) {
	a := map[string]interface{}{"namespace": "ns", "microserviceName": "svc", "scope": "service"}
	b := map[string]interface{}{"microserviceName": "svc", "scope": "service", "namespace": "ns"}
	assert.Equal(t, canonicalClassifier(a), canonicalClassifier(b))
}

func TestCanonicalClassifier_OmitsEmptyValues(t *testing.T) {
	with := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "service", "tenantId": ""}
	without := map[string]interface{}{"microserviceName": "svc", "namespace": "ns", "scope": "service"}
	assert.Equal(t, canonicalClassifier(with), canonicalClassifier(without))
}

func TestCanonicalClassifier_NestedCustomKeys(t *testing.T) {
	clf := map[string]interface{}{
		"microserviceName": "svc",
		"namespace":        "ns",
		"scope":            "service",
		"customKeys":       map[string]interface{}{"z": "1", "a": "2"},
	}
	key := canonicalClassifier(clf)
	// "a" must come before "z" in the output
	aIdx := indexOf(key, `"a"`)
	zIdx := indexOf(key, `"z"`)
	assert.True(t, aIdx < zIdx, "customKeys must be sorted: got %s", key)
}

func indexOf(s, sub string) int {
	return strings.Index(s, sub)
}

func TestMatchingKey_TypeLowercased(t *testing.T) {
	clf := serviceClassifier("svc", "ns")
	assert.Equal(t, matchingKey(clf, "PostgreSQL", ""), matchingKey(clf, "postgresql", ""))
}

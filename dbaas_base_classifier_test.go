package dbaasbase

import (
	"context"
	"github.com/netcracker/qubership-core-lib-go/v3/configloader"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/context-propagation/ctxmanager"
	"github.com/netcracker/qubership-core-lib-go/v3/context-propagation/baseproviders/tenant"
	. "github.com/netcracker/qubership-core-lib-go/v3/const"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"os"
	"testing"
)

const (
	microserviceName = "test_service"
	namespace        = "test_namespace"
)

var (
	tempTokenFile *os.File
)

func init() {
	ctxmanager.Register([]ctxmanager.ContextProvider{tenant.TenantProvider{}})
}

func beforeAll() {
	tempTokenFile, _ = ioutil.TempFile("", "test_token")
	tempTokenFile.Write([]byte("k8s-test-token"))
	tempTokenFile.Close()
	os.Setenv("kubertokenpath", tempTokenFile.Name())

	setUp()
}

func afterAll() {
	os.Remove(tempTokenFile.Name())
	os.Clearenv()
}

func TestMain(m *testing.M) {
	beforeAll()
	exitCode := m.Run()
	afterAll()
	os.Exit(exitCode)
}

func setUp() {
	serviceloader.Register(2, &security.DummyToken{})

	os.Setenv(MicroserviceNameProperty, microserviceName)
	os.Setenv(NamespaceProperty, namespace)
	configloader.Init(configloader.EnvPropertySource())
}

func tearDown() {
	os.Unsetenv(MicroserviceNameProperty)
	os.Unsetenv(NamespaceProperty)
}

func TestCreateServiceClassifierV3(t *testing.T) {
	defer tearDown()
	expected := map[string]interface{}{
		"microserviceName": "test_service",
		"scope":            "service",
		"namespace":        "test_namespace",
	}
	actual := BaseServiceClassifier(context.Background())
	assert.Equal(t, expected, actual)
}

func TestCreateTenantClassifierV3(t *testing.T) {
	ctx := createTenantContext()
	logger.Info("context: %v", ctx)
	expected := map[string]interface{}{
		"microserviceName": "test_service",
		"tenantId":         tenantId,
		"scope":            "tenant",
		"namespace":        "test_namespace",
	}
	actual := BaseTenantClassifier(ctx)
	assert.Equal(t, expected, actual)
}

func TestCreateTenantClassifier_WithoutTenantIdV3(t *testing.T) {
	ctx := context.Background()

	assert.Panics(t, func() {
		BaseTenantClassifier(ctx)
	})
}

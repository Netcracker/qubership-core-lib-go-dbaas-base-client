package dbaasbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	constants "github.com/netcracker/qubership-core-lib-go/v3/const"
	"github.com/netcracker/qubership-core-lib-go/v3/configloader"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	"github.com/netcracker/qubership-core-lib-go/v3/context-propagation/baseproviders/tenant"
	"github.com/netcracker/qubership-core-lib-go/v3/context-propagation/ctxmanager"
	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model"
	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model/rest"
	. "github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

const (
	dbaasAgentUrlEnvName         = "dbaas.agent"
	namespaceEnvName             = constants.NamespaceProperty
	microserviceEnvName          = constants.MicroserviceNameProperty
	k8SJWTPathEnvName            = "kubertokenpath"
	dbaasGetConnectionEndpointV3 = "/api/v3/dbaas/test_namespace/databases/get-by-classifier/type"
	dbaasGetOrCreateEndpointV3   = "/api/v3/dbaas/test_namespace/databases"
	testToken                    = "test-token"
	testTokenExpiresIn           = 300
	dbType                       = "type"
	tenantId                     = "tenant-id-123"
	dbaasApiVersionEndpoint      = "/api-version"
)

type DbaasClientTestSuite struct {
	suite.Suite
	classifier map[string]interface{}
	params     configloader.YamlPropertySourceParams
}

func (suite *DbaasClientTestSuite) SetupSuite() {
	serviceloader.Register(2, &security.DummyToken{})

	StartMockServer()
	os.Setenv(dbaasAgentUrlEnvName, GetMockServerUrl())
	os.Setenv(namespaceEnvName, "test_namespace")
	os.Setenv(microserviceEnvName, "test_service")
	os.Setenv(k8SJWTPathEnvName, "testdata/k8s_token")
	suite.params = configloader.YamlPropertySourceParams{ConfigFilePath: "testdata/application.yaml"}
	configloader.InitWithSourcesArray(configloader.BasePropertySources(suite.params))
	suite.classifier = map[string]interface{}{"scope": "service", "microserviceName": "test_service"}
}

func (suite *DbaasClientTestSuite) TearDownSuite() {
	os.Unsetenv(dbaasAgentUrlEnvName)
	os.Unsetenv(namespaceEnvName)
	os.Unsetenv(microserviceEnvName)
	StopMockServer()
}

func (suite *DbaasClientTestSuite) BeforeTest(suiteName, testName string) {
	suite.T().Cleanup(ClearHandlers)
}

func (suite *DbaasClientTestSuite) TestNewDbaasClient_WithoutOptions() {
	dbaasClient := NewDbaasClient()
	assert.NotNil(suite.T(), dbaasClient)
}

func (suite *DbaasClientTestSuite) TestNewDbaasClient_WithOptions() {
	options := model.СlientOptions{LogicalDbProviders: []model.LogicalDbProvider{testCorrectLogicalDbProvider{}}}
	dbaasClient := NewDbaasClient(options)
	assert.NotNil(suite.T(), dbaasClient)
	assert.NotNil(suite.T(), dbaasClient.options.LogicalDbProviders)
}

func (suite *DbaasClientTestSuite) TestGetConnection_ApiV3ExistsAndSetCorrectAnswer() {
	message := "connection_string"
	password := "qwerty"
	params := rest.BaseDbParams{}

	AddHandler(Contains(dbaasGetConnectionEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonGetConnectionResponse(password, message)
		writer.Write(jsonString)
	})

	dbClient := NewDbaasClient()
	actualMessage, err := dbClient.GetConnection(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), message, actualMessage["response"])
	assert.Equal(suite.T(), password, actualMessage["password"])
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase() {
	params := rest.BaseDbParams{}

	AddHandler(Contains("databases"), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonLogicalDbResponse(suite.classifier)
		writer.Write(jsonString)
	})

	dbClient := NewDbaasClient()
	actualLogicalDb, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), "1", actualLogicalDb.Id)
	assert.Equal(suite.T(), suite.classifier, actualLogicalDb.Classifier)
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase_UseLogicalDbProvider() {
	params := rest.BaseDbParams{}

	correctLogicalDbProvider := testCorrectLogicalDbProvider{testServerUrl: GetMockServerUrl()}
	options := model.СlientOptions{LogicalDbProviders: []model.LogicalDbProvider{correctLogicalDbProvider}}
	dbClient := NewDbaasClient(options)
	actualLogicalDb, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), "1", actualLogicalDb.Id)
	assert.Equal(suite.T(), suite.classifier, actualLogicalDb.Classifier)
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase_UseLogicalDbProviderFromList() {
	params := rest.BaseDbParams{}

	AddHandler(Contains("databases"), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonLogicalDbResponse(suite.classifier)
		writer.Write(jsonString)
	})

	correctLogicalDbProvider := testCorrectLogicalDbProvider{testServerUrl: GetMockServerUrl()}
	options := model.СlientOptions{
		LogicalDbProviders: []model.LogicalDbProvider{testNilLogicalDbProvider{}, correctLogicalDbProvider},
	}
	dbClient := NewDbaasClient(options)
	actualLogicalDb, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), "1", actualLogicalDb.Id)
	assert.Equal(suite.T(), suite.classifier, actualLogicalDb.Classifier)
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase_AllLogicalDbProviderReturnNil() {
	params := rest.BaseDbParams{}

	AddHandler(Contains("databases"), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonLogicalDbResponse(suite.classifier)
		writer.Write(jsonString)
	})

	options := model.СlientOptions{
		LogicalDbProviders: []model.LogicalDbProvider{testNilLogicalDbProvider{}, testNilLogicalDbProvider{}},
	}
	dbClient := NewDbaasClient(options)
	actualLogicalDb, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), "1", actualLogicalDb.Id)
	assert.Equal(suite.T(), suite.classifier, actualLogicalDb.Classifier)
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase_LogicalDbProviderReturnError() {
	params := rest.BaseDbParams{}

	AddHandler(Contains("databases"), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonLogicalDbResponse(suite.classifier)
		writer.Write(jsonString)
	})

	configloader.InitWithSourcesArray([]*configloader.PropertySource{configloader.EnvPropertySource()})
	options := model.СlientOptions{
		LogicalDbProviders: []model.LogicalDbProvider{testErrorLogicalDbProvider{}},
	}
	dbClient := NewDbaasClient(options)
	if _, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), err.Error(), "error during providing")
	}
}

func (suite *DbaasClientTestSuite) TestGetDatabase_GetConnectionFromProvider() {
	isRequestSent := false
	params := rest.BaseDbParams{}
	AddHandler(Contains(dbaasGetConnectionEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		isRequestSent = true
	})

	correctLogicalDbProvider := testCorrectLogicalDbProvider{testServerUrl: GetMockServerUrl()}
	options := model.СlientOptions{LogicalDbProviders: []model.LogicalDbProvider{correctLogicalDbProvider}}
	dbClient := NewDbaasClient(options)
	connection, err := dbClient.GetConnection(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.False(suite.T(), isRequestSent)
	assert.Equal(suite.T(), "test-password", connection["password"])
	assert.Equal(suite.T(), "test-username", connection["username"])
}

func (suite *DbaasClientTestSuite) TestGetDatabase_UseLogicalDbProviderFromListV3() {
	isRequestSent := false
	params := rest.BaseDbParams{}
	AddHandler(Contains(dbaasGetConnectionEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		isRequestSent = true
	})

	correctLogicalDbProvider := testCorrectLogicalDbProvider{testServerUrl: GetMockServerUrl()}
	options := model.СlientOptions{
		LogicalDbProviders: []model.LogicalDbProvider{testNilLogicalDbProvider{}, correctLogicalDbProvider},
	}
	dbClient := NewDbaasClient(options)
	connection, err := dbClient.GetConnection(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.False(suite.T(), isRequestSent)
	assert.Equal(suite.T(), "test-password", connection["password"])
	assert.Equal(suite.T(), "test-username", connection["username"])
}

func (suite *DbaasClientTestSuite) TestGetConnection_AllLogicalDbProviderReturnNilV3() {
	message := "connection_string"
	password := "qwerty"
	params := rest.BaseDbParams{}

	isRequestSent := false
	AddHandler(Contains(dbaasGetConnectionEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonGetConnectionResponse(password, message)
		writer.Write(jsonString)
		isRequestSent = true
	})

	options := model.СlientOptions{
		LogicalDbProviders: []model.LogicalDbProvider{testNilLogicalDbProvider{}, testNilLogicalDbProvider{}},
	}
	dbClient := NewDbaasClient(options)
	connection, err := dbClient.GetConnection(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.True(suite.T(), isRequestSent)
	assert.Equal(suite.T(), password, connection["password"])
	assert.Equal(suite.T(), message, connection["response"])
}

func (suite *DbaasClientTestSuite) TestGetConnection_LogicalDbProviderReturnError() {
	message := "connection_string"
	password := "qwerty"
	params := rest.BaseDbParams{}

	isRequestSent := false
	AddHandler(Contains(dbaasGetConnectionEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonGetConnectionResponse(password, message)
		writer.Write(jsonString)
		isRequestSent = true
	})

	configloader.InitWithSourcesArray([]*configloader.PropertySource{configloader.EnvPropertySource()})
	options := model.СlientOptions{
		LogicalDbProviders: []model.LogicalDbProvider{testErrorLogicalDbProvider{}},
	}
	dbClient := NewDbaasClient(options)
	if _, err := dbClient.GetConnection(context.Background(), dbType, suite.classifier, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), err.Error(), "error during providing")
		assert.False(suite.T(), isRequestSent)
	}
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase_DbaasNotReady() {
	yamlParams := configloader.YamlPropertySourceParams{ConfigFilePath: "testdata/application.yaml"}
	configloader.InitWithSourcesArray(configloader.BasePropertySources(yamlParams))
	params := rest.BaseDbParams{}

	AddHandler(Contains("databases"), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	})
	AddHandler(Contains(dbaasApiVersionEndpoint), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	dbClient := NewDbaasClient()
	if _, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), err.Error(), model.DbaaSCreateDbError{
			HttpCode: 500,
			Message:  "Failed to get response from DbaaS.",
			Errors:   nil,
		}.Error())
	}
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase_ClassifierIsEmpty() {
	dbClient := NewDbaasClient()
	params := rest.BaseDbParams{}
	if _, err := dbClient.GetOrCreateDb(context.Background(), dbType, map[string]interface{}{}, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), "classifier is not valid. \"microserviceName\" field must be not empty", err.Error())
	}
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabase_4xxDbaasError() {
	configloader.InitWithSourcesArray(configloader.BasePropertySources(suite.params))
	AddHandler(Contains(dbaasGetOrCreateEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})
	AddHandler(Contains(dbaasApiVersionEndpoint), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	params := rest.BaseDbParams{}
	dbClient := NewDbaasClient()
	if _, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), err.Error(), "Failed to get response from DbaaS")
	}
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabaseRetryPolicy() {
	configloader.InitWithSourcesArray(configloader.BasePropertySources(suite.params))
	AddHandler(Contains(dbaasGetOrCreateEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	})
	AddHandler(Contains(dbaasApiVersionEndpoint), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	params := rest.BaseDbParams{}
	dbClient := NewDbaasClient()
	if _, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), err.Error(), "Incorrect response from DbaaS. Stop retrying")
	}
}

func (suite *DbaasClientTestSuite) TestSendRequestToDbaas() {
	ctxmanager.Register([]ctxmanager.ContextProvider{tenant.TenantProvider{}})
	params := rest.BaseDbParams{}
	ctx := createTenantContext()

	AddHandler(Contains("databases"), func(writer http.ResponseWriter, request *http.Request) {
		assert.NotEmpty(suite.T(), request.Header.Get(tenant.TenantHeader))
		assert.Equal(suite.T(), tenantId, request.Header.Get(tenant.TenantHeader))
		writer.WriteHeader(http.StatusOK)
		jsonString := jsonLogicalDbResponse(suite.classifier)
		writer.Write(jsonString)
	})

	dbClient := NewDbaasClient()
	_, err := dbClient.GetOrCreateDb(ctx, dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
}

func (suite *DbaasClientTestSuite) TestSendRequestToDbaas_V3Return202() {
	configloader.InitWithSourcesArray(configloader.BasePropertySources(suite.params))
	counter := 0
	AddHandler(Contains(dbaasGetOrCreateEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		if counter < 3 {
			writer.WriteHeader(http.StatusAccepted)
			counter++
		} else {
			writer.WriteHeader(http.StatusOK)
			jsonString := jsonLogicalDbResponse(suite.classifier)
			writer.Write(jsonString)
		}
	})

	dbClient := NewDbaasClient()
	params := rest.BaseDbParams{}
	_, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), 3, counter) // check we had 3 retries
}

func (suite *DbaasClientTestSuite) TestSendRequestToDbaas_V3AlwaysReturn202() {
	configloader.InitWithSourcesArray(configloader.BasePropertySources(suite.params))

	AddHandler(Contains(dbaasGetOrCreateEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})
	AddHandler(Contains(dbaasApiVersionEndpoint), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	dbClient := NewDbaasClient()
	params := rest.BaseDbParams{}
	if _, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), err.Error(), "Failed to get response from DbaaS")
	}
}

func (suite *DbaasClientTestSuite) TestSendRequestToDbaas_RetriesOnNetworkProblems() {
	counter := 0
	AddHandler(Contains(dbaasGetOrCreateEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		if counter < 5 {
			counter++
			panic("Emulation of network problems")
		} else {
			writer.WriteHeader(http.StatusOK)
			jsonString := jsonLogicalDbResponse(suite.classifier)
			writer.Write(jsonString)
		}
	})
	AddHandler(Contains(dbaasApiVersionEndpoint), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	dbClient := NewDbaasClient()
	params := rest.BaseDbParams{}
	_, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params)
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), 5, counter) // check we had 5 retries on client.Do error
}

func (suite *DbaasClientTestSuite) TestSendRequestToDbaas_AlwaysNetworkProblems() {
	AddHandler(Contains(dbaasGetOrCreateEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		panic("Emulation of network problems")
	})

	dbClient := NewDbaasClient()
	params := rest.BaseDbParams{}
	if _, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params); assert.Error(suite.T(), err) {
		assert.Contains(suite.T(), err.Error(), "Failed to connect to dbaas")
	}
}

func (suite *DbaasClientTestSuite) TestGetOrCreateDatabaseDbaaSApiV3NotAvailable() {
	params := rest.BaseDbParams{}

	AddHandler(Contains(dbaasGetOrCreateEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})
	AddHandler(Contains(dbaasApiVersionEndpoint), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})

	dbClient := NewDbaasClient()
	actualLogicalDb, err := dbClient.GetOrCreateDb(context.Background(), dbType, suite.classifier, params)
	assert.NotNil(suite.T(), err)
	assert.Nil(suite.T(), actualLogicalDb)
	assert.Contains(suite.T(), err.Error(), "API v3 dbaas-aggregator is not available")
}

func (suite *DbaasClientTestSuite) TestGetConnectionDbaaSApiV3NotAvailable() {
	params := rest.BaseDbParams{}

	AddHandler(Contains(dbaasGetConnectionEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusRequestTimeout)
	})
	AddHandler(Contains(dbaasApiVersionEndpoint), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusRequestTimeout)
	})

	dbClient := NewDbaasClient()
	actualLogicalDb, err := dbClient.GetConnection(context.Background(), dbType, suite.classifier, params)
	assert.NotNil(suite.T(), err)
	assert.Nil(suite.T(), actualLogicalDb)
	assert.Contains(suite.T(), err.Error(), "API v3 dbaas-aggregator is not available")
}

func (suite *DbaasClientTestSuite) TestGetConnectionRetryPolicy() {
	params := rest.BaseDbParams{}

	AddHandler(Contains(dbaasGetConnectionEndpointV3), func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})

	dbClient := NewDbaasClient()
	actualLogicalDb, err := dbClient.GetConnection(context.Background(), dbType, suite.classifier, params)
	assert.NotNil(suite.T(), err)
	assert.Nil(suite.T(), actualLogicalDb)
	assert.Contains(suite.T(), err.Error(), "Incorrect response from DbaaS. Stop retrying")
}

func (suite *DbaasClientTestSuite) TestGetOrCreateWithWrongClassifier() {
	params := rest.BaseDbParams{}
	classifier := map[string]interface{}{"microserviceName": "test_service"}

	dbClient := NewDbaasClient()
	actualLogicalDb, err := dbClient.GetOrCreateDb(context.Background(), dbType, classifier, params)
	assert.NotNil(suite.T(), err)
	assert.Nil(suite.T(), actualLogicalDb)
	assert.Contains(suite.T(), err.Error(), "Classifier is not valid.")
}

func (suite *DbaasClientTestSuite) TestIsValidClassifier_ErrorNotValid() {
	classifier := map[string]interface{}{"microserviceName": "test_service", "namespace": "test_namespace"}
	err := isValidClassifier(context.Background(), classifier)
	assert.NotNil(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "Classifier is not valid.")
}

func (suite *DbaasClientTestSuite) TestIsValidClassifier_ErrorClassifierIsEmpty() {
	classifier := map[string]interface{}{}
	err := isValidClassifier(context.Background(), classifier)
	assert.NotNil(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "classifier can't be nil or empty")
}

func (suite *DbaasClientTestSuite) TestIsValidClassifier_ErrorNotValidNamespace() {
	classifier := map[string]interface{}{"microserviceName": "test_service"}
	err := isValidClassifier(context.Background(), classifier)
	assert.NotNil(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "classifier is not valid. \"namespace\" field must be not empty")
}

func (suite *DbaasClientTestSuite) TestIsValidClassifier_Valid() {
	classifier := map[string]interface{}{"microserviceName": "test_service", "scope": "service", "namespace": "test_namespace"}
	err := isValidClassifier(context.Background(), classifier)
	assert.Nil(suite.T(), err)
}

func (suite *DbaasClientTestSuite) TestIsValidClassifier_ErrorClassifierWithoutTenantId() {
	classifier := map[string]interface{}{"microserviceName": "test_service", "scope": "tenant", "namespace": "test_namespace"}
	err := isValidClassifier(context.Background(), classifier)
	assert.NotNil(suite.T(), err)
	assert.Contains(suite.T(), err.Error(), "classifier is not valid.")
}

func TestSuite(t *testing.T) {
	suite.Run(t, new(DbaasClientTestSuite))
}

func jsonGetConnectionResponse(pwd, message string) []byte {
	mapResponse := map[string]interface{}{
		"password": pwd,
		"response": message,
	}
	logicalDb := &model.LogicalDb{
		ConnectionProperties: mapResponse,
	}
	jsonResp, _ := json.Marshal(logicalDb)
	return jsonResp
}

func jsonLogicalDbResponse(classifier map[string]interface{}) []byte {
	mapResponse := map[string]interface{}{
		"password": "qwerty",
	}
	logicalDb := &model.LogicalDb{
		Id:                   "1",
		Classifier:           classifier,
		ConnectionProperties: mapResponse,
		Namespace:            "test-namespace",
		Type:                 dbType,
		Settings:             nil,
	}
	jsonResp, _ := json.Marshal(logicalDb)
	return jsonResp
}

func createTenantContext() context.Context {
	incomingHeaders := map[string]interface{}{tenant.TenantHeader: tenantId}
	return ctxmanager.InitContext(context.Background(), incomingHeaders)
}

// Test implementations of LogicalDbProvider
type testCorrectLogicalDbProvider struct {
	testServerUrl string
}

func (t testCorrectLogicalDbProvider) GetOrCreateDb(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*model.LogicalDb, error) {
	connProperties := map[string]interface{}{
		"host":      t.testServerUrl,
		"namespace": "test-namespace",
	}
	return &model.LogicalDb{
		Id:                   "1",
		Classifier:           classifier,
		ConnectionProperties: connProperties,
		Namespace:            "test-namespace",
		Type:                 dbType,
		Settings:             nil,
	}, nil
}

func (t testCorrectLogicalDbProvider) GetConnection(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	connProperties := map[string]interface{}{
		"host":      t.testServerUrl,
		"namespace": "test-namespace",
		"password":  "test-password",
		"username":  "test-username",
	}
	return connProperties, nil
}

type testErrorLogicalDbProvider struct{}

func (t testErrorLogicalDbProvider) GetOrCreateDb(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*model.LogicalDb, error) {
	return &model.LogicalDb{}, errors.New("error during providing")
}

func (t testErrorLogicalDbProvider) GetConnection(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	return nil, errors.New("error during providing")
}

type testNilLogicalDbProvider struct{}

func (t testNilLogicalDbProvider) GetOrCreateDb(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*model.LogicalDb, error) {
	return nil, nil
}

func (t testNilLogicalDbProvider) GetConnection(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	return nil, nil
}

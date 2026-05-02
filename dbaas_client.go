package dbaasbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	intermodel "github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/internal/model"
	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model"
	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model/rest"
	"github.com/netcracker/qubership-core-lib-go/v3/configloader"
	"github.com/netcracker/qubership-core-lib-go/v3/const"
	"github.com/netcracker/qubership-core-lib-go/v3/logging"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
	restclient "github.com/netcracker/qubership-core-lib-go/v3/security/rest"
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
)

var logger logging.Logger

const (
	getOrCreateDatabaseV3     = "%s/api/v3/dbaas/%s/databases"
	getDatabaseByClassifierV3 = "%s/api/v3/dbaas/%s/databases/get-by-classifier/%s"
	apiVersion                = "%s/api-version"
	MsgClassifierIsNotValid   = "Can't create database with wrong classifier: %+v"
)

func init() {
	logger = logging.GetLogger("dbaasbase")
}

type DbaaSClient interface {
	GetOrCreateDb(ctx context.Context, dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*model.LogicalDb, error)
	GetConnection(ctx context.Context, dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error)
}

type dbaasClientImpl struct {
	options       model.ClientOptions
	dbaasAgentUrl string
	namespace     string
	client        restclient.Client
}

func NewDbaasClient(options ...model.ClientOptions) *dbaasClientImpl {
	defaultDbaasAgentUrl := constants.SelectUrl("http://dbaas-agent:8080", "https://dbaas-agent:8443")
	dbsAgentUrl := configloader.GetOrDefaultString("dbaas.agent", defaultDbaasAgentUrl)
	namespace := configloader.GetKoanf().MustString("microservice.namespace")
	dbsClntImpl := &dbaasClientImpl{
		dbaasAgentUrl: dbsAgentUrl,
		namespace:     namespace,
		client:        restclient.NewDbaasRestClient(),
	}

	if options != nil {
		dbsClntImpl.options = options[0]
	} else {
		dbsClntImpl.options = model.ClientOptions{}
	}
	return dbsClntImpl
}

func (d *dbaasClientImpl) GetOrCreateDb(ctx context.Context, dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*model.LogicalDb, error) {
	classifier = d.enrichClassifier(classifier)
	err := isValidClassifier(ctx, classifier)
	if err != nil {
		return nil, err
	}
	dbCreateReq := rest.CreateDbRequest{
		BaseDbParams: params,
		Classifier:   classifier,
		Type:         dbType,
	}
	providedLogicalDb, err := getDbFromProviders(d.options.LogicalDbProviders, &dbCreateReq)
	if err != nil {
		return nil, err
	}
	if providedLogicalDb != nil {
		logger.Debugf("Using logicalDbProvider with classifier %+v and type %v for dbaasUrl creation", providedLogicalDb.Classifier, providedLogicalDb.Type)
		return providedLogicalDb, nil
	}

	dbaasUrlV3 := fmt.Sprintf(getOrCreateDatabaseV3, d.dbaasAgentUrl, d.namespace)

	logger.Info("Requesting database from DBaaS with createRequest: %+v", dbCreateReq)
	contents, err := d.sendRequestToDbaaSWithRetry(ctx, dbaasUrlV3, dbCreateReq, http.MethodPut, &intermodel.NoRetryPolicy)
	if err != nil {
		return nil, err
	}

	logicalDB := &model.LogicalDb{}
	if err := json.Unmarshal(contents, logicalDB); err != nil {
		logger.Error("Unable to unmarshall json with response from DBaaS ")
		return nil, err
	}

	logger.Infof("Database with connectionUrl = %s with classifier: %+v and type = %s was successfully got or created ", logicalDB.ConnectionProperties["url"], logicalDB.Classifier, logicalDB.Type)
	return logicalDB, nil
}

func getDbFromProviders(providers []model.LogicalDbProvider, createDbRequest *rest.CreateDbRequest) (*model.LogicalDb, error) {
	for _, provider := range providers {
		providedLogicalDb, err := provider.GetOrCreateDb(createDbRequest.Type, createDbRequest.Classifier, createDbRequest.BaseDbParams)
		if err != nil {
			logger.Errorf("Error during LogicalDb providing: %+v", err)
			return nil, err
		}
		if providedLogicalDb != nil {
			return providedLogicalDb, nil
		}
	}
	return nil, nil
}

func (d *dbaasClientImpl) GetConnection(ctx context.Context, dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	dbaasUrlV3 := fmt.Sprintf(getDatabaseByClassifierV3, d.dbaasAgentUrl, d.namespace, dbType)
	classifier = d.enrichClassifier(classifier)
	var connectionProperties map[string]interface{}
	connectionProperties, err := getConnectionFromProviders(d.options.LogicalDbProviders, dbType, classifier, params)
	if err != nil {
		return nil, err
	}
	reqParams := make(map[string]interface{})
	reqParams["classifier"] = classifier
	if &params.Role != nil && len(params.Role) > 0 {
		reqParams["userRole"] = params.Role
	}
	if connectionProperties == nil {
		contents, err := d.sendRequestToDbaaSWithRetry(ctx, dbaasUrlV3, reqParams, http.MethodPost, &intermodel.NoRetryPolicyForGetConnection)
		if err != nil {
			return nil, err
		}

		responseBody := make(map[string]interface{})
		if err := json.Unmarshal(contents, &responseBody); err != nil {
			logger.Error("Unable to unmarshall response body")
			return nil, err
		}
		logger.Debugf("Returning connection to base with classifier: %s", responseBody["classifier"])
		connectionProperties = responseBody["connectionProperties"].(map[string]interface{})
	}

	return connectionProperties, nil
}

func getConnectionFromProviders(providers []model.LogicalDbProvider, dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	for _, provider := range providers {
		providedLogicalDb, err := provider.GetConnection(dbType, classifier, params)
		if err != nil {
			logger.Errorf("Error during LogicalDb providing: %+v", err)
			return nil, err
		}
		if providedLogicalDb != nil {
			logger.Debugf("Using logicalDbProvider with classifier %+v and type %v for dbaasUrl creation", classifier, dbType)
			return providedLogicalDb, nil
		}
	}
	return nil, nil
}

func (d *dbaasClientImpl) enrichClassifier(classifier map[string]interface{}) map[string]interface{} {
	if classifier["namespace"] == nil {
		classifier["namespace"] = d.namespace
	}
	return classifier
}

func (d *dbaasClientImpl) sendRequestToDbaaSWithRetry(ctx context.Context, dbaasUrl string, requestBody interface{}, httpMethod string, retryPolicy *intermodel.RetryPolicy) ([]byte, error) {
	tokenProvider := serviceloader.MustLoad[security.TokenProvider]()
	token, err := tokenProvider.GetToken(ctx)
	if err != nil {
		logger.ErrorC(ctx, "Some problems during getting m2m token: %v", err.Error())
		return nil, fmt.Errorf("some problems during getting m2m token: %w", err)
	}
	requestPayload, err := json.Marshal(requestBody)
	if err != nil {
		logger.ErrorC(ctx, "Got error during marshaling connection request: %v", err.Error())
		return nil, fmt.Errorf("got error during marshaling connection request: %w", err)
	}

	resp, err := d.retryRequestToDbaaS(ctx, dbaasUrl, httpMethod, requestPayload, token, retryPolicy)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contents, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.ErrorC(ctx, "Error occurred during response body reading: %v", err.Error())
		return nil, model.DbaaSCreateDbError{
			HttpCode: resp.StatusCode,
			Message:  "Error occurred during response body reading.",
			Errors:   err,
		}
	}
	logger.DebugC(ctx, "Got response from DBaaS with code : %d ", resp.StatusCode)
	return contents, nil
}

func (d *dbaasClientImpl) retryRequestToDbaaS(ctx context.Context, dbaasUrl string, httpMethod string, requestPayload []byte, token string, retryPolicy *intermodel.RetryPolicy) (*http.Response, error) {
	hasBeenInterrupted := false
	maxNumberOfAttempts := configloader.GetOrDefault("dbaas.baseclient.retry.max-attempts", 12).(int)
	delay := configloader.GetOrDefault("dbaas.baseclient.retry.delay-ms", 5000).(int)
	var resp *http.Response
	for i := 0; i <= maxNumberOfAttempts; i++ {
		var err error
		resp, err = d.client.DoRequest(ctx, httpMethod, dbaasUrl, map[string][]string{
			"Content-Type": []string{"application/json"},
		}, bytes.NewReader(requestPayload))
		if err != nil {
			logger.WarnC(ctx, "Error during sending request to dbaas: %v", err.Error())
			if i == maxNumberOfAttempts {
				errMsg := "Failed to connect to dbaas."
				if resp != nil && resp.StatusCode == http.StatusAccepted {
					errMsg = fmt.Sprintf("Database was not created during a timeout of %d seconds.", maxNumberOfAttempts*delay/1000)
				}
				return nil, model.DbaaSCreateDbError{
					HttpCode: 000,
					Message:  errMsg,
					Errors:   fmt.Errorf("dbaas error: %w", err),
				}
			} else {
				time.Sleep(time.Duration(delay) * time.Millisecond)
				continue
			}
		}

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
			break
		} else if retryPolicy.HasNotRetryableHttpCode(resp.StatusCode) {
			logger.ErrorC(ctx, "Request to %s %s failed with status code %d", httpMethod, dbaasUrl, resp.StatusCode)
			hasBeenInterrupted = true
		} else if resp.StatusCode == http.StatusAccepted {
			logger.InfoC(ctx, "Secure %s request to %s got status code %d, database is not created yet, retrying %d out of %d", httpMethod, dbaasUrl, resp.StatusCode, i, maxNumberOfAttempts)
			time.Sleep(time.Duration(delay) * time.Millisecond)
		} else if resp.StatusCode >= 300 {
			logger.WarnC(ctx, "Request to %s %s failed with status code %d, retrying %d out of %d", httpMethod, dbaasUrl, resp.StatusCode, i, maxNumberOfAttempts)
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}

		if i == maxNumberOfAttempts || hasBeenInterrupted {
			err = d.checkDbaasApiVersion(ctx)
			if err != nil {
				return nil, model.DbaaSCreateDbError{
					HttpCode: resp.StatusCode,
					Message:  "API v3 dbaas-aggregator is not available",
					Errors:   err,
				}
			}
			responseBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			errMsg := "Failed to get response from DbaaS."
			if hasBeenInterrupted {
				errMsg = "Incorrect response from DbaaS. Stop retrying"
			}
			return nil, model.DbaaSCreateDbError{
				HttpCode: resp.StatusCode,
				Message:  errMsg,
				Errors:   errors.New(fmt.Sprintf("request to DbaaS failed with response body: %s", responseBody)),
			}
		}
	}
	return resp, nil
}

func isValidClassifier(ctx context.Context, classifier map[string]interface{}) error {
	var err error
	if classifier == nil || len(classifier) == 0 {
		logger.ErrorC(ctx, "Can't create database or get connection property with empty classifier")
		return errors.New("classifier can't be nil or empty")
	}
	if classifier["microserviceName"] == nil {
		err = errors.New("classifier is not valid. \"microserviceName\" field must be not empty")
		logger.ErrorC(ctx, MsgClassifierIsNotValid, err.Error())
		return errors.New("classifier is not valid. \"microserviceName\" field must be not empty")
	}
	if classifier["namespace"] == nil {
		err = errors.New("classifier is not valid. \"namespace\" field must be not empty")
		logger.ErrorC(ctx, MsgClassifierIsNotValid, err.Error())
		return errors.New("classifier is not valid. \"namespace\" field must be not empty")
	}
	if classifier["scope"] != "service" {
		if classifier["scope"] == "tenant" {
			if classifier["tenantId"] != nil {
				return nil
			}
			err = errors.New("classifier is not valid. Tenant classifier must contain \"tenantId\": \"<tenant_id>\"")
			logger.ErrorC(ctx, MsgClassifierIsNotValid, err.Error())
			return err

		}
		err = errors.New("Classifier is not valid. Service classifier must contain \"scope\": \"service\"." +
			" Tenant classifier must contain  \"scope\": \"tenant\" and \"tenantId\": \"<tenant_id>\"")
		logger.ErrorC(ctx, MsgClassifierIsNotValid, err.Error())
		return err
	}
	return nil
}

func (d *dbaasClientImpl) checkDbaasApiVersion(ctx context.Context) error {
	dbaasUrl := fmt.Sprintf(apiVersion, d.dbaasAgentUrl)
	logger.DebugC(ctx, "Send request to: %s", dbaasUrl)
	resp, err := d.client.DoRequest(ctx, http.MethodGet, dbaasUrl, map[string][]string{}, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		logger.DebugC(ctx, "DbaaS v3 is available.")
		return nil
	}
	err = errors.New("API v3 dbaas-aggregator is not available")
	logger.ErrorC(ctx, "%+v. Go dbaas client v1 only works with following requirements: "+
		"DbaaS version 3.18.0 or later, Cloud - Core release-6-62-0-20220816.084650-20-RELEASE or later.", err)
	return err
}

[![Go build](https://github.com/Netcracker/qubership-core-lib-go-dbaas-base-client/actions/workflows/go-build.yml/badge.svg)](https://github.com/Netcracker/qubership-core-lib-go-dbaas-base-client/actions/workflows/go-build.yml)
[![Coverage](https://sonarcloud.io/api/project_badges/measure?metric=coverage&project=Netcracker_qubership-core-lib-go-dbaas-base-client)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-lib-go-dbaas-base-client)
[![duplicated_lines_density](https://sonarcloud.io/api/project_badges/measure?metric=duplicated_lines_density&project=Netcracker_qubership-core-lib-go-dbaas-base-client)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-lib-go-dbaas-base-client)
[![vulnerabilities](https://sonarcloud.io/api/project_badges/measure?metric=vulnerabilities&project=Netcracker_qubership-core-lib-go-dbaas-base-client)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-lib-go-dbaas-base-client)
[![bugs](https://sonarcloud.io/api/project_badges/measure?metric=bugs&project=Netcracker_qubership-core-lib-go-dbaas-base-client)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-lib-go-dbaas-base-client)
[![code_smells](https://sonarcloud.io/api/project_badges/measure?metric=code_smells&project=Netcracker_qubership-core-lib-go-dbaas-base-client)](https://sonarcloud.io/summary/overall?id=Netcracker_qubership-core-lib-go-dbaas-base-client)

# Dbaas base-client

Basic and not database specific (does not provide any db driver) DBaaS REST API client implementation. 
Allows acquiring raw databases connections.

* [Install](#install)
* [Usage](#usage)
  - [Configuration](#configuration)
  - [LogicalDbProviders](#logicaldbproviders)
  - [LogicalDb](#logicaldb)
  - [BaseDbParams](#basedbparams)
* [Quick example](#quick-example)


## Install

To get `dbaasbase` use
```go
 go get github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3@<latest released version>
```

List of all released versions may be found [here](https://github.com/netcracker/go/dbaas/base-client/-/tags)

## Usage

At first, it's necessary to register security implemention - dummy or your own, the followning example shows registration of required services:
```go
import (
	"github.com/netcracker/qubership-core-lib-go/v3/serviceloader"
	"github.com/netcracker/qubership-core-lib-go/v3/security"
)

func init() {
	serviceloader.Register(1, &security.DummyToken{})
}
```

Then you have to create `dbaasbase.DbaasPool` object with constructor `dbaasbase.NewDbaaSPool(options ...PoolOptions) *DbaaSPool`.
Constructor has optional parameter `PoolOptions`.

PoolOptions are options for configuring dbaas pool and base dbaas client, which will be used with dbaasPool. PoolOptions
has such fields as:

* LogicalDbProviders []LogicalDbProvider - list of possible logicalDb providers. See more info at [LogicalDbProviders](#logicaldbproviders)

Example of dbaasPool creation:
```go
    dbPool := dbaasbase.NewDbaasPool() // without options
    ...
    // or with some client options
    opts := dbaasbase.PoolOptions{
    	LogicalDbProviders : []dbaasbase.LogicalDbProvider{customProvider}
    }   
    dbPool := dbaasbase.NewDbaasPool(opts)
```

DbaasPool has next API:

* `GetOrCreateDb(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*LogicalDb, error)`

  This function allows getting information about database and collect it into [LogicalDb](#logicaldb) struct.  This function
  will return info about existing database or create new one and return connection to it.

  Parameters:
  - _ctx context.Context_ - golang request scope context object.
  - _dbType string_ - type of database, e.g. cassandra, postgresql, mongodb.
  - _classifier map[string]interface{}_ - Composite uniq key. It distinguishes this database from other databases in the same namespace.
  - _params rest.BaseDbParams_ - some extra not required parameters for specific database creation. More info at [BaseDbParams](#basedbparams)


* `GetConnection(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error)`

  This function allows getting information about connection and get response as _map[string]interface{}_. Please note,
  that this method doesn't use cache and always send request to dbaas-aggregator. Also, it won't create a database. If
  database doesn't exist func will just return nil.

  Parameters:
  - _ctx context.Context_ - golang request scope context object.
  - _dbType string_ - type of database, e.g. cassandra, postgresql, mongodb.
  - _classifier map[string]interface{}_ - describes the purpose of the database, and it distinguishes this database from other databases in the same namespace.
  - _params rest.BaseDbParams_ - some extra not required parameters for specific database creation and getting connection. More info at [BaseDbParams](#basedbparams)
### Configuration

|Name|Description|Optional|Default|Since|
|---|---|---|---|---|
|microservice.name                    | Name of current microservice (eg. tenant-manager)                                                                                    | false  | -  | 0.1.0 |
|microservice.namespace               | Name of current namespace                                                                                                            | false  | -  | 0.1.0 |
|dbaas.baseclient.retry.max-attempts  | Number of retry attempts                                                                                                             | true   | 12 | 0.1.0 |
|dbaas.baseclient.retry.delay-ms      | Delay per attempt (ms)                                                                                                               | true   | 5000 | 0.1.0 |


### LogicalDbProviders

LogicalDbProvider allows use different sources as database providers (for example zookeeper or some localy created database). Default databases source is _dbaas-aggregator_.

To add another database source user, **at first**, have to implement `LogicalDbProvider` interface. 
```go
type LogicalDbProvider interface {
    GetOrCreateDb(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*LogicalDb, error)
    GetConnection(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error)
}
```

* Func `GetOrCreateDb` must return [LogicalDb](#logicaldb) with mandatory `connectionProperties` value.
* Func `GetConnection` must return map[string]interface{} with information about connection properties (like password, username, connection string, etc.)

**Then** user have to create `PoolOptions` object with list of created LogicalDbProviders and pass this object as a parameter to
`NewDbaaSPool(options ...PoolOptions)`. Now when user call any `DbaasPool` method, LogicalDbProviders from list will be used
as new connection sources.

Depending on the presence of LogicalDbProviders, the behavior of the module differs.
* There are no LogicalDbProviders. Module will load information from dbaas-aggregator.
* There are some LogicalDbProviders. Module will first use LogicalDbProvider from the list in the passed order. 
  If each `LogicalDbProvider` returns nil then logical database will be created through dbaas-aggregator.
* There are some LogicalDbProviders and some LogicalDbProvider returns **error**.
  In this case module will stop executing the function and return an error.
* There are some LogicalDbProviders and first LogicalDbProvider in the list returns **nil** when `GetOrCreateDb` or `GetConnection` was called.   
  In this case module will switch to the next provider in the list. If all providers run out, the module will go back to using
  dbaas-aggregator.

Example of custom LogicalDbProvider creation:

```go
package main

import "github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3"

type CustomLogicalDbProvider struct{}

func (CLDB CustomLogicalDbProvider) GetOrCreateDb(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*dbaasbase.LogicalDb, error) {
  connectionProperties, err := getConnectionProperties()
  if err != nil {
    return nil, err
  }
  
  logicalDb := &dbaasbase.LogicalDb{
    Classifier:           classifier,
    ConnectionProperties: connectionProperties,
    Namespace:            getNamespace(),
    Type:                 dbType,
  }
  return logicalDb, nil
}

func (CLDB CustomLogicalDbProvider) GetConnection(dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
  connectionProperties, err := getConnectionProperties()
  return connectionProperties, err
}

func main() {
  options := dbaasbase.PoolOptions{LogicalDbProviders: []dbaasbase.LogicalDbProvider{CustomLogicalDbProvider{}}}
  dbPool := dbaasbase.NewDbaaSPool(options)
}
```

### LogicalDb

LogicalDb is a way to store information about databases locally, it is a representation of dbaas-aggregator response.

LogicalDb has such fields as:

|Name| Description                                                                                                                                                                                                                                                               |Schema|
|---|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---|
|**classifier**  | Classifier describes the purpose of the database and it distinguishes this database from other databases in the same namespace. It contains such keys as dbClassifier, scope (service or tenant), microserviceName, namespace. Setting keys depends on the database type. |map[string]interface{}|
|**connectionProperties**  | This is an information about connection to database. It contains such keys as url, authDbName, username, password, port, host.Setting keys depends on the database type.                                                                                                  |map[string]interface{}|
|**id**  | A unique identifier of the document in the database. This field might not be used when searching by classifier for security purpose. And it exists in the response when executing Create database API                                                                     |string|
|**namespace** | Namespace where database is placed.                                                                                                                                                                                                                                       |string|
|**settings**  | Additional settings for creating a database.                                                                                                                                                                                                                              |map[string]interface{}|
|**type** | Type of database, for example PostgreSQL or MongoDB                                                                                                                                                                                                                       |string|

### BaseDbParams

`BaseDbParams` allows customizing database creation and getting connection.

| Name                  | Description                                                                                                                                                                                                                                                                       |Schema|
|-----------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|---|
| **userRole**          | Indicates connection properties with which user role should be returned to a client and indicates if it has rights to create a database. Default is admin.                                                                                                                        |string|
| **namePrefix**        | This is a prefix of the database name. Prefix depends on the type of the database and it should be less than 27 characters if dbName is not specified.                                                                                                                            |string|
| **physicalDatabaseId** | Specifies the identificator of physical database where a logical database will be created. If it is not specified then logical database will be created in default physical database. You can get the list of all physical databases by "List registered physical databases" API. |string|
| **settings**          | Additional settings for creating database. There is a possibility to update settings after database creation.                                                                                                                                                                     |object|

## Quick example

```go
package main

import (
  "fmt"
  "github.com/netcracker/qubership-core-lib-go/v3/logging"
  "github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/dbaasbase"
)

var logger logging.Logger

func init() {
  logger = logging.GetLogger("main")
}

func main() {
  dbaasPool := dbaasbase.NewDbaaSPool()

  classifier := make(map[string]interface{})
  classifier["scope"] = "service"
  classifier["microserviceName"] = "service_name"

  settings := make(map[string]interface{})
  listOfExtensions := []string{"bloom", "pgcrypt"}
  settings["pgExtensions"] = listOfExtensions

  params := dbaasbase.BaseDbParams{
    NamePrefix:         "test_db",
    Settings:           settings,
	userRole:           "admin",   //optional
  }
  
  // create new database of type postgres with classifier and params
  logicalDb, err := dbaasPool.CreateOrGetDatabase("postgresql", classifier, params)
  if err != nil {
    logger.Error("Problem with database creation")
  }
  fmt.Println(logicalDb)
  
  // acquire connection to created database
  conn, err := dbaasPool.GetConnection("postgresql", classifier, params)
  if err != nil {
    logger.Errorf("Problem with acquiring connection to db with classifier %+v", classifier)
  }
  fmt.Println(conn)
}
```

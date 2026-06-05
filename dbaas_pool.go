package dbaasbase

import (
	"context"

	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/cache"
	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model"
	"github.com/netcracker/qubership-core-lib-go-dbaas-base-client/v3/model/rest"
	"github.com/netcracker/qubership-core-lib-go/v3/configloader"
)

type DbaaSPool struct {
	poolCache *cache.DbaaSCache
	Client    DbaaSClient
}

func NewDbaaSPool(options ...model.PoolOptions) *DbaaSPool {
	poolCache := &cache.DbaaSCache{LogicalDbCache: make(map[cache.Key]interface{})}
	var providers []model.LogicalDbProvider
	if options != nil {
		providers = options[0].LogicalDbProviders
	}
	if configloader.GetKoanf().Bool(MountedSecretEnabledKey) {
		basePath := configloader.GetOrDefaultString(MountedSecretBasePathKey, mountedSecretDefaultPath)
		providers = append(providers, newMountedSecretProvider(basePath))
	}
	client := NewDbaasClient(model.ClientOptions{LogicalDbProviders: providers})
	return &DbaaSPool{poolCache: poolCache, Client: client}
}

func (p *DbaaSPool) GetOrCreateDb(ctx context.Context, dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (*model.LogicalDb, error) {
	key := cache.NewKey(dbType, classifier)
	db, err := p.poolCache.Cache(key, func() (interface{}, error) {
		return p.Client.GetOrCreateDb(ctx, dbType, classifier, params)
	})
	if err != nil {
		logger.Errorf("Can neither create db with classifier %+v nor get it from poolCache", classifier)
		return nil, err
	}
	return db.(*model.LogicalDb), nil
}

func (p *DbaaSPool) GetConnection(ctx context.Context, dbType string, classifier map[string]interface{}, params rest.BaseDbParams) (map[string]interface{}, error) {
	connection, err := p.Client.GetConnection(ctx, dbType, classifier, params)
	if err != nil {
		logger.Errorf("Can not get connection to db with classifier %+v", classifier)
		return nil, err
	}
	return connection, nil
}

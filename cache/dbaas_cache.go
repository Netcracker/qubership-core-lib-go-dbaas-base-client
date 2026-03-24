package cache

import (
	"encoding/json"
	"sync"

	"github.com/netcracker/qubership-core-lib-go/v3/logging"
)

type Key struct {
	DbType        string
	Classifier    string
	discriminator string
}

type DbaaSCache struct {
	Mx             sync.RWMutex
	LogicalDbCache map[Key]interface{}
}

type Discriminator interface {
	GetValue() string
}

var logger logging.Logger

func init() {
	logger = logging.GetLogger("dbaasbase")
}

func NewKey(dbType string, classifier map[string]interface{}) Key {
	classifierAsString, _ := json.Marshal(classifier)
	key := Key{
		DbType:     dbType,
		Classifier: string(classifierAsString),
	}
	return key
}

func NewKeyWithDiscriminator(dbType string, classifier map[string]interface{}, discriminator Discriminator) Key {
	key := NewKey(dbType, classifier)
	key.discriminator = discriminator.GetValue()
	return key
}

func (d *DbaaSCache) Cache(key Key, calc func() (interface{}, error)) (interface{}, error) {
	d.Mx.RLock()
	if val, ok := d.LogicalDbCache[key]; ok {
		logger.Debugf("Got existing database with type %v and classifier %+v", key.DbType, key.Classifier)
		defer d.Mx.RUnlock()
		return val, nil
	} else {
		d.Mx.RUnlock()
		d.Mx.Lock()
		defer d.Mx.Unlock()
		if val, ok = d.LogicalDbCache[key]; ok {
			logger.Debugf("Got existing database with type %v and classifier %+v", key.DbType, key.Classifier)
			return val, nil
		}
		logger.Infof("Create new database with type %v and classifier %+v", key.DbType, key.Classifier)
		if val, err := calc(); err == nil {
			d.LogicalDbCache[key] = val
			return val, nil
		} else {
			logger.Errorf("Error during call db request to dbaas: %v", err.Error())
			return nil, err
		}
	}
}

func (d *DbaaSCache) Delete(key Key) {
	d.Mx.Lock()
	defer d.Mx.Unlock()
	logger.Infof("Delete database with type %v and classifier %+v from cache", key.DbType, key.Classifier)
	delete(d.LogicalDbCache, key)
}

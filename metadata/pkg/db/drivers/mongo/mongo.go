// Copyright 2019 The OpenSDS Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mongo

import (
	"context"
	"errors"
	"sync"

	"github.com/micro/go-micro/v2/metadata"
	log "github.com/sirupsen/logrus"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/opensds/multi-cloud/api/pkg/common"
	"github.com/opensds/multi-cloud/metadata/pkg/model"
)

var adap = &adapter{}
var mutex sync.Mutex
var MetadataDataBaseName = "metadatastore"
var MetadataCollectionName = "metadatabucket"
var mongodb = "mongodb://"

func Init(host string) *adapter {
	mutex.Lock()
	defer mutex.Unlock()

	if adap.session != nil {
		return adap
	}
	// Create a new client and connect to the server
	uri := mongodb + host
	client, err := mongo.Connect(context.TODO(), options.Client().ApplyURI(uri))
	if err != nil {
		panic(err)
	}
	if err := client.Ping(context.TODO(), readpref.Primary()); err != nil {
		panic(err)
	}
	log.Infoln("Successfully connected and pinged from metadata service.")

	adap.session = client

	return adap
}

func Exit() {
	adap.session.Disconnect(context.TODO())
}

type adapter struct {
	session *mongo.Client
}

func UpdateContextFilter(ctx context.Context, m bson.M) error {
	// if context is admin, no need filter by tenantId.
	md, ok := metadata.FromContext(ctx)
	if !ok {
		log.Error("get context failed")
		return errors.New("get context failed")
	}

	isAdmin := md[common.CTX_KEY_IS_ADMIN]
	if isAdmin != common.CTX_VAL_TRUE {
		tenantId, ok := md[common.CTX_KEY_TENANT_ID]
		if !ok {
			log.Error("get tenantid failed")
			return errors.New("get tenantid failed")
		}
		m["tenantid"] = tenantId
	}

	return nil
}

func (ad *adapter) CreateMetadata(ctx context.Context, metaBackend model.MetaBackend) error {
	session := ad.session
	filter := bson.M{"_id": metaBackend.Id}
	upsert := true
	options := options.ReplaceOptions{Upsert: &upsert}

	_, err := session.Database(MetadataDataBaseName).Collection(MetadataCollectionName).ReplaceOne(ctx, filter, metaBackend, &options)

	if err != nil {
		log.Errorf("failed to sync metadata for backend id: %v. failed with error: %v", metaBackend.Id, err)
		return err
	}

	log.Infof("metadata successfully synced for backend id: %v", metaBackend.Id)
	return nil
}

func (ad *adapter) ListMetadata(ctx context.Context, query []bson.D) ([]*model.MetaBackend, error) {
	log.Infoln("received list metadata request")
	session := ad.session

	//TODO: change database and collection name
	pipeline := mongo.Pipeline(query)

	// pass the pipeline to the Aggregate() method

	log.Debugln("pipeline query:", pipeline)

	database := session.Database(MetadataDataBaseName)
	collection := database.Collection(MetadataCollectionName)

	log.Debugln("database:", database.Name())
	log.Debugln("collection name:", collection.Name())

	cur, err := session.Database(MetadataDataBaseName).Collection(MetadataCollectionName).Aggregate(ctx, pipeline)
	if err != nil {
		log.Errorf("Failed to execute query in database: %v", err)
		return nil, err
	}

	//Map result to slice
	var results []*model.MetaBackend = make([]*model.MetaBackend, 0)
	if err = cur.All(context.TODO(), &results); err != nil {
		log.Errorf("Error constructing model.MetaBucket objects from database result: %v", err)
		return nil, err
	}
	return results, nil
}

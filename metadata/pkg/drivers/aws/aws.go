// Copyright 2023 The SODA Authors.
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

package aws

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/globalsign/mgo/bson"
	backendpb "github.com/opensds/multi-cloud/backend/proto"
	"github.com/opensds/multi-cloud/metadata/pkg/db"
	"github.com/opensds/multi-cloud/metadata/pkg/model"
	pb "github.com/opensds/multi-cloud/metadata/proto"
	log "github.com/sirupsen/logrus"
)

type S3Cred struct {
	Ak string
	Sk string
}

func (myc *S3Cred) IsExpired() bool {
	return false
}

func (myc *S3Cred) Retrieve() (credentials.Value, error) {
	cred := credentials.Value{AccessKeyID: myc.Ak, SecretAccessKey: myc.Sk}
	return cred, nil
}

type AwsAdapter struct {
	Backend *backendpb.BackendDetail
	Session *session.Session
}

func ObjectList(sess *session.Session, bucket *model.MetaBucket) {
	svc := s3.New(sess, aws.NewConfig().WithRegion(bucket.Region))
	output, err := svc.ListObjectsV2(&s3.ListObjectsV2Input{Bucket: &bucket.Name})
	if err != nil {
		log.Errorf("unable to list objects in bucket %v. failed with error: %v", bucket.Name, err)
		return
	}

	numObjects := len(output.Contents)
	var totSize int64
	totSize = 0
	objectArray := make([]*model.MetaObject, numObjects)
	for objIdx, object := range output.Contents {
		obj := &model.MetaObject{}
		objectArray[objIdx] = obj
		obj.LastModifiedDate = *object.LastModified
		obj.ObjectName = *object.Key
		obj.Size = *object.Size
		totSize += obj.Size
		obj.StorageClass = *object.StorageClass

		meta, err := svc.HeadObject(&s3.HeadObjectInput{Bucket: &bucket.Name, Key: object.Key})
		if err != nil {
			log.Errorf("cannot perform head object on object %v in bucket %v. failed with error: %v", *object.Key, bucket.Name, err)
			return
		}
		if meta.ServerSideEncryption != nil {
			obj.ServerSideEncryption = *meta.ServerSideEncryption
		}
		if meta.VersionId != nil {
			obj.VersionId = *meta.VersionId
		}
		obj.ObjectType = *meta.ContentType
		if meta.Expires != nil {
			expiresTime, err := time.Parse(time.RFC3339, *meta.Expires)
			if err != nil {
				log.Errorf("unable to parse given string to time type. error: %v. skipping ExpiresDate field", err)
			} else {
				obj.ExpiresDate = expiresTime
			}
		}
		if meta.ReplicationStatus != nil {
			obj.ReplicationStatus = *meta.ReplicationStatus
		}
	}
	bucket.NumberOfObjects = numObjects
	bucket.TotalSize = totSize
	bucket.Objects = objectArray
}

func GetBucketMeta(buckIdx int, bucket *s3.Bucket, sess *session.Session, bucketArray []*model.MetaBucket, wg *sync.WaitGroup) error {
	defer wg.Done()
	buck := &model.MetaBucket{}
	bucketArray[buckIdx] = buck
	buck.CreationDate = *bucket.CreationDate
	buck.Name = *bucket.Name
	svc := s3.New(sess)
	loc, err := svc.GetBucketLocation(&s3.GetBucketLocationInput{Bucket: bucket.Name})
	if err != nil {
		log.Errorf("unable to get bucket location. failed with error: %v", err)
		return err
	}

	buck.Region = *loc.LocationConstraint

	if *sess.Config.Region != buck.Region {
		svc = s3.New(sess, aws.NewConfig().WithRegion(buck.Region))
	}

	tags, err := svc.GetBucketTagging(&s3.GetBucketTaggingInput{Bucket: bucket.Name})

	if err != nil && !strings.Contains(err.Error(), "NoSuchTagSet") {
		log.Errorf("unable to get bucket tags. failed with error: %v", err)
	} else {
		tagset := make(map[string]string)
		for _, tag := range tags.TagSet {
			tagset[*tag.Key] = *tag.Value
		}
		buck.BucketTags = tagset
	}

	ObjectList(sess, buck)
	if err != nil {
		return err
	}
	return nil
}

func BucketList(sess *session.Session) ([]*model.MetaBucket, error) {
	svc := s3.New(sess)

	output, err := svc.ListBuckets(&s3.ListBucketsInput{})
	if err != nil {
		log.Errorf("unable to list buckets. failed with error: %v", err)
		return nil, err
	}
	numBuckets := len(output.Buckets)
	bucketArray := make([]*model.MetaBucket, numBuckets)
	wg := sync.WaitGroup{}
	for i, bucket := range output.Buckets {
		wg.Add(1)
		go GetBucketMeta(i, bucket, sess, bucketArray, &wg)
	}
	wg.Wait()
	return bucketArray, err
}

func (ad *AwsAdapter) SyncMetadata(ctx context.Context, in *pb.SyncMetadataRequest) error {

	buckArr, err := BucketList(ad.Session)
	if err != nil {
		log.Errorf("metadata collection for backend id: %v failed with error: %v", ad.Backend.Id, err)
		return err
	}

	metaBackend := model.MetaBackend{}
	metaBackend.Id = bson.ObjectId(ad.Backend.Id)
	metaBackend.BackendName = ad.Backend.Name
	metaBackend.Type = ad.Backend.Type
	metaBackend.Region = ad.Backend.Region
	metaBackend.Buckets = buckArr
	newContext := context.TODO()
	err = db.DbAdapter.CreateMetadata(newContext, metaBackend)

	return err
}

func (ad *AwsAdapter) DownloadObject() {
	log.Info("Implement me")
}
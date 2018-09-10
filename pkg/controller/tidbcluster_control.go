// Copyright 2018 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"fmt"
	"strings"

	"github.com/golang/glog"
	"github.com/pingcap/tidb-operator/pkg/apis/pingcap.com/v1alpha1"
	"github.com/pingcap/tidb-operator/pkg/client/clientset/versioned"
	tcinformers "github.com/pingcap/tidb-operator/pkg/client/informers/externalversions/pingcap.com/v1alpha1"
	listers "github.com/pingcap/tidb-operator/pkg/client/listers/pingcap.com/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
)

// TidbClusterControlInterface manages TidbClusters
type TidbClusterControlInterface interface {
	UpdateTidbCluster(*v1alpha1.TidbCluster) (*v1alpha1.TidbCluster, error)
}

type realTidbClusterControl struct {
	cli      versioned.Interface
	tcLister listers.TidbClusterLister
	recorder record.EventRecorder
}

// NewRealTidbClusterControl creates a new TidbClusterControlInterface
func NewRealTidbClusterControl(cli versioned.Interface,
	tcLister listers.TidbClusterLister,
	recorder record.EventRecorder) TidbClusterControlInterface {
	return &realTidbClusterControl{
		cli,
		tcLister,
		recorder,
	}
}

func (rtc *realTidbClusterControl) UpdateTidbCluster(tc *v1alpha1.TidbCluster) (*v1alpha1.TidbCluster, error) {
	ns := tc.GetNamespace()
	tcName := tc.GetName()

	status := tc.Status.DeepCopy()
	// pdReplicas := tc.Spec.PD.Replicas
	// tikvReplicas := tc.Spec.TiKV.Replicas
	// tidbReplicas := tc.Spec.TiDB.Replicas
	var updateTC *v1alpha1.TidbCluster

	// don't wait due to limited number of clients, but backoff after the default number of steps
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		tc.Status = *status
		// tc.Spec.PD.Replicas = pdReplicas
		// tc.Spec.TiKV.Replicas = tikvReplicas
		// tc.Spec.TiDB.Replicas = tidbReplicas
		var updateErr error
		updateTC, updateErr = rtc.cli.PingcapV1alpha1().TidbClusters(ns).Update(tc)
		if updateErr == nil {
			glog.Infof("TidbCluster: [%s/%s] updated successfully", ns, tcName)
			return nil
		}
		glog.Errorf("failed to update TidbCluster: [%s/%s], error: %v", ns, tcName, updateErr)

		if updated, err := rtc.tcLister.TidbClusters(ns).Get(tcName); err == nil {
			// make a copy so we don't mutate the shared cache
			tc = updated.DeepCopy()
		} else {
			utilruntime.HandleError(fmt.Errorf("error getting updated TidbCluster %s/%s from lister: %v", ns, tcName, err))
		}

		return updateErr
	})
	rtc.recordTidbClusterEvent("update", tc, err)
	return updateTC, err
}

func (rtc *realTidbClusterControl) recordTidbClusterEvent(verb string, tc *v1alpha1.TidbCluster, err error) {
	tcName := tc.GetName()
	if err == nil {
		reason := fmt.Sprintf("Successful%s", strings.Title(verb))
		msg := fmt.Sprintf("%s TidbCluster %s successful",
			strings.ToLower(verb), tcName)
		rtc.recorder.Event(tc, corev1.EventTypeNormal, reason, msg)
	} else {
		reason := fmt.Sprintf("Failed%s", strings.Title(verb))
		msg := fmt.Sprintf("%s TidbCluster %s failed error: %s",
			strings.ToLower(verb), tcName, err)
		rtc.recorder.Event(tc, corev1.EventTypeWarning, reason, msg)
	}
}

// FakeTidbClusterControl is a fake TidbClusterControlInterface
type FakeTidbClusterControl struct {
	TcLister                 listers.TidbClusterLister
	TcIndexer                cache.Indexer
	updateTidbClusterTracker requestTracker
}

// NewFakeTidbClusterControl returns a FakeTidbClusterControl
func NewFakeTidbClusterControl(tcInformer tcinformers.TidbClusterInformer) *FakeTidbClusterControl {
	return &FakeTidbClusterControl{
		tcInformer.Lister(),
		tcInformer.Informer().GetIndexer(),
		requestTracker{0, nil, 0},
	}
}

// SetUpdateTidbClusterError sets the error attributes of updateTidbClusterTracker
func (ssc *FakeTidbClusterControl) SetUpdateTidbClusterError(err error, after int) {
	ssc.updateTidbClusterTracker.err = err
	ssc.updateTidbClusterTracker.after = after
}

// UpdateTidbCluster updates the TidbCluster
func (ssc *FakeTidbClusterControl) UpdateTidbCluster(tc *v1alpha1.TidbCluster) (*v1alpha1.TidbCluster, error) {
	defer ssc.updateTidbClusterTracker.inc()
	if ssc.updateTidbClusterTracker.errorReady() {
		defer ssc.updateTidbClusterTracker.reset()
		return tc, ssc.updateTidbClusterTracker.err
	}

	return tc, ssc.TcIndexer.Update(tc)
}

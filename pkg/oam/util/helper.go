/*
Copyright 2021 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"encoding/json"
	"fmt"
	"hash"
	"hash/fnv"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	cpv1alpha1 "github.com/crossplane/crossplane-runtime/apis/core/v1alpha1"
	"github.com/davecgh/go-spew/spew"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/common"
	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1alpha2"
	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	"github.com/oam-dev/kubevela/pkg/oam"
	"github.com/oam-dev/kubevela/pkg/oam/discoverymapper"
)

var (
	// KindDeployment is the k8s Deployment kind.
	KindDeployment = reflect.TypeOf(appsv1.Deployment{}).Name()
	// KindService is the k8s Service kind.
	KindService = reflect.TypeOf(corev1.Service{}).Name()
	// ReconcileWaitResult is the time to wait between reconciliation.
	ReconcileWaitResult = reconcile.Result{RequeueAfter: 30 * time.Second}
)

const (
	// TraitPrefixKey is prefix of trait name
	TraitPrefixKey = "trait"

	// Dummy used for dummy definition
	Dummy = "dummy"

	// DummyTraitMessage is a message for trait which don't have definition found
	DummyTraitMessage = "No TraitDefinition found, all framework capabilities will work as default"

	// DefinitionNamespaceEnv is env key for specifying a namespace to fetch definition
	DefinitionNamespaceEnv = "DEFINITION_NAMESPACE"
)

const (
	// ErrUpdateStatus is the error while applying status.
	ErrUpdateStatus = "cannot apply status"
	// ErrLocateAppConfig is the error while locating parent application.
	ErrLocateAppConfig = "cannot locate the parent application configuration to emit event to"
	// ErrLocateWorkload is the error while locate the workload
	ErrLocateWorkload = "cannot find the workload that the trait is referencing to"
	// ErrFetchChildResources is the error while fetching workload child resources
	ErrFetchChildResources = "failed to fetch workload child resources"

	errFmtGetComponentRevision   = "cannot get component revision %q"
	errFmtControllerRevisionData = "cannot get valid component data from controllerRevision %q"
	errFmtGetComponent           = "cannot get component %q"
	errFmtInvalidRevisionType    = "invalid type of revision %s, type should not be %v"

	// ErrStoreCapabilityInConfigMap is the error while storing capability in ConfigMap
	ErrStoreCapabilityInConfigMap = "cannot store capability %s in ConfigMap: %v"
	// ErrGenerateOpenAPIV2JSONSchemaForCapability is the error while generating OpenAPI v3 schema
	ErrGenerateOpenAPIV2JSONSchemaForCapability = "cannot generate OpenAPI v3 JSON schema for capability %s: %v"
	// ErrUpdateCapabilityInConfigMap is the error while creating or updating a capability
	ErrUpdateCapabilityInConfigMap = "cannot create or update capability %s in ConfigMap: %v"

	// ErrUpdateComponentDefinition is the error while update ComponentDefinition
	ErrUpdateComponentDefinition = "cannot update ComponentDefinition %s: %v"
	// ErrUpdateTraitDefinition is the error while update TraitDefinition
	ErrUpdateTraitDefinition = "cannot update TraitDefinition %s: %v"
	// ErrUpdatePolicyDefinition is the error while update PolicyDefinition
	ErrUpdatePolicyDefinition = "cannot update PolicyDefinition %s: %v"
	// ErrUpdateWorkflowStepDefinition is the error while update WorkflowStepDefinition
	ErrUpdateWorkflowStepDefinition = "cannot update WorkflowStepDefinition %s: %v"

	// ErrCreateConvertedWorklaodDefinition is the error while apply a WorkloadDefinition
	ErrCreateConvertedWorklaodDefinition = "cannot create converted WorkloadDefinition %s: %v"

	// ErrRefreshPackageDiscover is the error while refresh PackageDiscover
	ErrRefreshPackageDiscover = "cannot discover the open api of the CRD : %v"

	// ErrGenerateDefinitionRevision is the error while generate DefinitionRevision
	ErrGenerateDefinitionRevision = "cannot generate DefinitionRevision of %s: %v"
	// ErrCreateOrUpdateDefinitionRevision is the error while create or update DefinitionRevision
	ErrCreateOrUpdateDefinitionRevision = "cannot create or update DefinitionRevision %s: %v"
)

// WorkloadType describe the workload type of ComponentDefinition
type WorkloadType string

const (
	// ComponentDef describe a workload of Defined by ComponentDefinition
	ComponentDef WorkloadType = "ComponentDef"

	// KubeDef describe a workload refer to raw K8s resource
	KubeDef WorkloadType = "KubeDef"

	// HELMDef describe a workload refer to HELM
	HELMDef WorkloadType = "HelmDef"

	// ReferWorkload describe an existing workload
	ReferWorkload WorkloadType = "ReferWorkload"
)

type namespaceContextKey int

const (
	// AppDefinitionNamespace is context key to define app namespace
	AppDefinitionNamespace namespaceContextKey = iota
)

// A ConditionedObject is an Object type with condition field
type ConditionedObject interface {
	oam.Object

	oam.Conditioned
}

// ErrBadRevisionName represents an error when the revision name is not standardized
var ErrBadRevisionName = fmt.Errorf("bad revision name")

// LocateParentAppConfig locate the parent application configuration object
func LocateParentAppConfig(ctx context.Context, client client.Client, oamObject oam.Object) (oam.Object, error) {

	// locate the appConf name from the owner list
	for _, o := range oamObject.GetOwnerReferences() {
		if o.Kind == v1alpha2.ApplicationConfigurationKind {
			var eventObj = &v1alpha2.ApplicationConfiguration{}
			acName := o.Name
			if len(acName) > 0 {
				nn := types.NamespacedName{
					Name:      acName,
					Namespace: oamObject.GetNamespace(),
				}
				if err := client.Get(ctx, nn, eventObj); err != nil {
					return nil, err
				}
				return eventObj, nil
			}
		}
		if o.Kind == v1alpha2.ApplicationContextKind {
			var eventObj = &v1alpha2.ApplicationContext{}
			appName := o.Name
			if len(appName) > 0 {
				nn := types.NamespacedName{
					Name:      appName,
					Namespace: oamObject.GetNamespace(),
				}
				if err := client.Get(ctx, nn, eventObj); err != nil {
					return nil, err
				}
				return eventObj, nil
			}
		}
	}
	return nil, errors.Errorf(ErrLocateAppConfig)
}

// FetchWorkload fetch the workload that a trait refers to
func FetchWorkload(ctx context.Context, c client.Client, mLog logr.Logger, oamTrait oam.Trait) (
	*unstructured.Unstructured, error) {
	var workload unstructured.Unstructured
	workloadRef := oamTrait.GetWorkloadReference()
	if len(workloadRef.Kind) == 0 || len(workloadRef.APIVersion) == 0 || len(workloadRef.Name) == 0 {
		err := errors.New("no workload reference")
		mLog.Error(err, ErrLocateWorkload)
		return nil, err
	}
	workload.SetAPIVersion(workloadRef.APIVersion)
	workload.SetKind(workloadRef.Kind)
	wn := client.ObjectKey{Name: workloadRef.Name, Namespace: oamTrait.GetNamespace()}
	if err := c.Get(ctx, wn, &workload); err != nil {
		mLog.Error(err, "Workload not find", "kind", workloadRef.Kind, "workload name", workloadRef.Name)
		return nil, err
	}
	mLog.Info("Get the workload the trait is pointing to", "workload name", workload.GetName(),
		"workload APIVersion", workload.GetAPIVersion(), "workload Kind", workload.GetKind(), "workload UID",
		workload.GetUID())
	return &workload, nil
}

// GetDummyTraitDefinition will generate a dummy TraitDefinition for CustomResource that won't block app from running.
// OAM runtime will report warning if they got this dummy definition.
func GetDummyTraitDefinition(u *unstructured.Unstructured) *v1alpha2.TraitDefinition {
	return &v1alpha2.TraitDefinition{
		TypeMeta: metav1.TypeMeta{Kind: v1alpha2.TraitDefinitionKind, APIVersion: v1alpha2.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Name: Dummy, Annotations: map[string]string{
			"apiVersion": u.GetAPIVersion(),
			"kind":       u.GetKind(),
			"name":       u.GetName(),
		}},
		Spec: v1alpha2.TraitDefinitionSpec{Reference: common.DefinitionReference{Name: Dummy}},
	}
}

// GetDummyWorkloadDefinition will generate a dummy WorkloadDefinition for CustomResource that won't block app from running.
// OAM runtime will report warning if they got this dummy definition.
func GetDummyWorkloadDefinition(u *unstructured.Unstructured) *v1alpha2.WorkloadDefinition {
	return &v1alpha2.WorkloadDefinition{
		TypeMeta: metav1.TypeMeta{Kind: v1alpha2.WorkloadDefinitionKind, APIVersion: v1alpha2.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{Name: Dummy, Annotations: map[string]string{
			"apiVersion": u.GetAPIVersion(),
			"kind":       u.GetKind(),
			"name":       u.GetName(),
		}},
		Spec: v1alpha2.WorkloadDefinitionSpec{Reference: common.DefinitionReference{Name: Dummy}},
	}
}

// FetchScopeDefinition fetch corresponding scopeDefinition given a scope
func FetchScopeDefinition(ctx context.Context, r client.Reader, dm discoverymapper.DiscoveryMapper,
	scope *unstructured.Unstructured) (*v1alpha2.ScopeDefinition, error) {
	// The name of the scopeDefinition CR is the CRD name of the scope
	// TODO(wonderflow): we haven't support scope definition label type yet.
	spName, err := GetDefinitionName(dm, scope, "")
	if err != nil {
		return nil, err
	}
	scopeDefinition := new(v1alpha2.ScopeDefinition)
	if err = GetDefinition(ctx, r, scopeDefinition, spName); err != nil {
		return nil, err
	}
	return scopeDefinition, nil
}

// FetchTraitDefinition fetch corresponding traitDefinition given a trait
func FetchTraitDefinition(ctx context.Context, r client.Reader, dm discoverymapper.DiscoveryMapper,
	trait *unstructured.Unstructured) (*v1alpha2.TraitDefinition, error) {
	// The name of the traitDefinition CR is the CRD name of the trait
	trName, err := GetDefinitionName(dm, trait, oam.TraitTypeLabel)
	if err != nil {
		return nil, err
	}
	traitDefinition := new(v1alpha2.TraitDefinition)
	if err = GetDefinition(ctx, r, traitDefinition, trName); err != nil {
		return nil, err
	}
	return traitDefinition, nil
}

// FetchWorkloadDefinition fetch corresponding workloadDefinition given a workload
func FetchWorkloadDefinition(ctx context.Context, r client.Reader, dm discoverymapper.DiscoveryMapper,
	workload *unstructured.Unstructured) (*v1alpha2.WorkloadDefinition, error) {
	// The name of the workloadDefinition CR is the CRD name of the component
	wldName, err := GetDefinitionName(dm, workload, oam.WorkloadTypeLabel)
	if err != nil {
		return nil, err
	}
	workloadDefinition := new(v1alpha2.WorkloadDefinition)
	if err = GetDefinition(ctx, r, workloadDefinition, wldName); err != nil {
		return nil, err
	}
	return workloadDefinition, nil
}

// GetDefinitionNamespaceWithCtx will get namespace from context, it will try get `AppDefinitionNamespace` key, if not found,
// will use default system level namespace defined in `systemvar.SystemDefinitonNamespace`
func GetDefinitionNamespaceWithCtx(ctx context.Context) string {
	var appNs string
	if app := ctx.Value(AppDefinitionNamespace); app == nil {
		appNs = oam.SystemDefinitonNamespace
	} else {
		appNs = app.(string)
	}
	return appNs
}

// SetNamespaceInCtx set app namespace in context,
// Sometimes webhook handler may receive request that appNs is empty string, and will cause error when search definition
// So if namespace is empty, it will use `default` namespace by default.
func SetNamespaceInCtx(ctx context.Context, namespace string) context.Context {
	if namespace == "" {
		// compatible with some webhook handlers that maybe receive empty string as app namespace which means `default` namespace
		namespace = "default"
	}
	ctx = context.WithValue(ctx, AppDefinitionNamespace, namespace)
	return ctx
}

// GetDefinition get definition from two level namespace
func GetDefinition(ctx context.Context, cli client.Reader, definition runtime.Object, definitionName string) error {
	if dns := os.Getenv(DefinitionNamespaceEnv); dns != "" {
		if err := cli.Get(ctx, types.NamespacedName{Name: definitionName, Namespace: dns}, definition); err == nil {
			return nil
		} else if !apierrors.IsNotFound(err) {
			return err
		}
	}
	appNs := GetDefinitionNamespaceWithCtx(ctx)
	if err := cli.Get(ctx, types.NamespacedName{Name: definitionName, Namespace: appNs}, definition); err != nil {
		if apierrors.IsNotFound(err) {
			if err = cli.Get(ctx, types.NamespacedName{Name: definitionName, Namespace: oam.SystemDefinitonNamespace}, definition); err != nil {
				if apierrors.IsNotFound(err) {
					// compatibility code for old clusters those definition crd is cluster scope
					var newErr error
					if newErr = cli.Get(ctx, types.NamespacedName{Name: definitionName}, definition); checkRequestNamespaceError(newErr) {
						return err
					}
					return newErr
				}
				return err
			}
			return err
		}
		return err
	}
	return nil
}

// GetCapabilityDefinition can get different versions of ComponentDefinition/TraitDefinition
func GetCapabilityDefinition(ctx context.Context, cli client.Reader, definition runtime.Object,
	definitionName string) error {
	isLatestRevision, defRev, err := fetchDefinitionRev(ctx, cli, definitionName)
	if err != nil {
		return err
	}
	if isLatestRevision {
		return GetDefinition(ctx, cli, definition, definitionName)
	}
	switch def := definition.(type) {
	case *v1beta1.ComponentDefinition:
		*def = defRev.Spec.ComponentDefinition
	case *v1beta1.TraitDefinition:
		*def = defRev.Spec.TraitDefinition
	case *v1beta1.PolicyDefinition:
		*def = defRev.Spec.PolicyDefinition
	case *v1beta1.WorkflowStepDefinition:
		*def = defRev.Spec.WorkflowStepDefinition
	default:
	}
	return nil
}

func fetchDefinitionRev(ctx context.Context, cli client.Reader, definitionName string) (bool, *v1beta1.DefinitionRevision, error) {
	defRevName, err := ConvertDefinitionRevName(definitionName)
	if err != nil {
		if errors.As(err, &ErrBadRevisionName) {
			return true, nil, nil
		}
		return false, nil, err
	}
	defRev := new(v1beta1.DefinitionRevision)
	if err = GetDefinition(ctx, cli, defRev, defRevName); err != nil {
		return false, nil, err
	}
	return false, defRev, err
}

// ConvertDefinitionRevName can help convert definition type defined in Application to DefinitionRevision Name
// e.g., worker@v2 will be convert to worker-v2
func ConvertDefinitionRevName(definitionName string) (string, error) {
	revNum, err := ExtractRevisionNum(definitionName, "@")
	if err != nil {
		return "", err
	}
	defName := strings.TrimSuffix(definitionName, fmt.Sprintf("@v%d", revNum))
	if defName == "" {
		return "", fmt.Errorf("invalid definition defName %s", definitionName)
	}
	return fmt.Sprintf("%s-v%d", defName, revNum), nil
}

// when get a  namespaced scope object without namespace, would get an error request namespace
func checkRequestNamespaceError(err error) bool {
	return err != nil && err.Error() == "an empty namespace may not be set when a resource name is provided"
}

// FetchWorkloadChildResources fetch corresponding child resources given a workload
func FetchWorkloadChildResources(ctx context.Context, mLog logr.Logger, r client.Reader,
	dm discoverymapper.DiscoveryMapper, workload *unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	// Fetch the corresponding workloadDefinition CR
	workloadDefinition, err := FetchWorkloadDefinition(ctx, r, dm, workload)
	if err != nil {
		// No definition will won't block app from running
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return fetchChildResources(ctx, mLog, r, workload, workloadDefinition.Spec.ChildResourceKinds)
}

func fetchChildResources(ctx context.Context, mLog logr.Logger, r client.Reader, workload *unstructured.Unstructured,
	wcrl []common.ChildResourceKind) ([]*unstructured.Unstructured, error) {
	var childResources []*unstructured.Unstructured
	// list by each child resource type with namespace and possible label selector
	for _, wcr := range wcrl {
		crs := unstructured.UnstructuredList{}
		crs.SetAPIVersion(wcr.APIVersion)
		crs.SetKind(wcr.Kind)
		mLog.Info("List child resource kind", "APIVersion", wcr.APIVersion, "Type", wcr.Kind, "owner UID",
			workload.GetUID())
		if err := r.List(ctx, &crs, client.InNamespace(workload.GetNamespace()),
			client.MatchingLabels(wcr.Selector)); err != nil {
			mLog.Error(err, "failed to list object", "api version", crs.GetAPIVersion(), "kind", crs.GetKind())
			return nil, err
		}
		// pick the ones that is owned by the workload
		for _, cr := range crs.Items {
			for _, owner := range cr.GetOwnerReferences() {
				if owner.UID == workload.GetUID() {
					mLog.Info("Find a child resource we are looking for",
						"APIVersion", cr.GetAPIVersion(), "Kind", cr.GetKind(),
						"Name", cr.GetName(), "owner", owner.UID)
					or := cr // have to do a copy as the range variable is a reference and will change
					childResources = append(childResources, &or)
				}
			}
		}
	}
	return childResources, nil
}

// PatchCondition condition for a conditioned object
func PatchCondition(ctx context.Context, r client.StatusClient, workload ConditionedObject,
	condition ...cpv1alpha1.Condition) error {
	workloadPatch := client.MergeFrom(workload.DeepCopyObject())
	workload.SetConditions(condition...)
	return errors.Wrap(
		r.Status().Patch(ctx, workload, workloadPatch, client.FieldOwner(workload.GetUID())),
		ErrUpdateStatus)
}

// A metaObject is a Kubernetes object that has label and annotation
type labelAnnotationObject interface {
	GetLabels() map[string]string
	SetLabels(labels map[string]string)
	GetAnnotations() map[string]string
	SetAnnotations(annotations map[string]string)
}

// PassLabel passes through labels from the parent to the child object
func PassLabel(parentObj, childObj labelAnnotationObject) {
	// pass app-config labels
	childObj.SetLabels(MergeMapOverrideWithDst(parentObj.GetLabels(), childObj.GetLabels()))
}

// PassLabelAndAnnotation passes through labels and annotation objectMeta from the parent to the child object
// when annotation or labels has conflicts, the parentObj will override the childObj.
func PassLabelAndAnnotation(parentObj, childObj labelAnnotationObject) {
	// pass app-config labels
	childObj.SetLabels(MergeMapOverrideWithDst(childObj.GetLabels(), parentObj.GetLabels()))
	// pass app-config annotation
	childObj.SetAnnotations(MergeMapOverrideWithDst(childObj.GetAnnotations(), parentObj.GetAnnotations()))
}

// RemoveLabels removes keys that contains in the removekeys slice from the label
func RemoveLabels(o labelAnnotationObject, removeKeys []string) {
	exist := o.GetLabels()
	for _, key := range removeKeys {
		delete(exist, key)
	}
	o.SetLabels(exist)
}

// RemoveAnnotations removes keys that contains in the removekeys slice from the annotation
func RemoveAnnotations(o labelAnnotationObject, removeKeys []string) {
	exist := o.GetAnnotations()
	for _, key := range removeKeys {
		delete(exist, key)
	}
	o.SetAnnotations(exist)
}

// GetDefinitionName return the Definition name of any resources
// the format of the definition of a resource is <kind plurals>.<group>
// Now the definition name of a resource could also be defined as `definition.oam.dev/name` in `metadata.annotations`
// typeLabel specified which Definition it is, if specified, will directly get definition from label.
func GetDefinitionName(dm discoverymapper.DiscoveryMapper, u *unstructured.Unstructured, typeLabel string) (string, error) {
	if typeLabel != "" {
		if labels := u.GetLabels(); labels != nil {
			if definitionName, ok := labels[typeLabel]; ok {
				return definitionName, nil
			}
		}
	}
	groupVersion, err := schema.ParseGroupVersion(u.GetAPIVersion())
	if err != nil {
		return "", err
	}
	mapping, err := dm.RESTMapping(schema.GroupKind{Group: groupVersion.Group, Kind: u.GetKind()}, groupVersion.Version)
	if err != nil {
		return "", err
	}
	return mapping.Resource.Resource + "." + groupVersion.Group, nil
}

// GetGVKFromDefinition help get Group Version Kind from DefinitionReference
func GetGVKFromDefinition(dm discoverymapper.DiscoveryMapper, definitionRef common.DefinitionReference) (schema.GroupVersionKind, error) {
	// if given definitionRef is empty or it's a dummy definition, return an empty GVK
	// NOTE currently, only TraitDefinition is allowed to omit definitionRef conditionally.
	if len(definitionRef.Name) < 1 || definitionRef.Name == Dummy {
		return schema.EmptyObjectKind.GroupVersionKind(), nil
	}
	var gvk schema.GroupVersionKind
	groupResource := schema.ParseGroupResource(definitionRef.Name)
	gvr := schema.GroupVersionResource{Group: groupResource.Group, Resource: groupResource.Resource, Version: definitionRef.Version}
	kinds, err := dm.KindsFor(gvr)
	if err != nil {
		return gvk, err
	}
	if len(kinds) < 1 {
		return gvk, &meta.NoResourceMatchError{
			PartialResource: gvr,
		}
	}
	return kinds[0], nil
}

// ConvertWorkloadGVK2Definition help convert a GVK to DefinitionReference
func ConvertWorkloadGVK2Definition(dm discoverymapper.DiscoveryMapper, def common.WorkloadGVK) (common.DefinitionReference, error) {
	var reference common.DefinitionReference
	gv, err := schema.ParseGroupVersion(def.APIVersion)
	if err != nil {
		return reference, err
	}
	gvk := gv.WithKind(def.Kind)
	gvr, err := dm.ResourcesFor(gvk)
	if err != nil {
		return reference, err
	}
	reference.Version = gvr.Version
	reference.Name = gvr.GroupResource().String()
	return reference, nil
}

// GetObjectsGivenGVKAndLabels fetches the kubernetes object given its gvk and labels by list API
func GetObjectsGivenGVKAndLabels(ctx context.Context, cli client.Reader,
	gvk schema.GroupVersionKind, namespace string, labels map[string]string) (*unstructured.UnstructuredList, error) {
	unstructuredObjList := &unstructured.UnstructuredList{}
	apiVersion := metav1.GroupVersion{
		Group:   gvk.Group,
		Version: gvk.Version,
	}.String()
	unstructuredObjList.SetAPIVersion(apiVersion)
	unstructuredObjList.SetKind(gvk.Kind)
	if err := cli.List(ctx, unstructuredObjList, client.MatchingLabels(labels), client.InNamespace(namespace)); err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to get obj with labels %+v and gvk %+v ", labels, gvk))
	}
	return unstructuredObjList, nil
}

// GetObjectGivenGVKAndName fetches the kubernetes object given its gvk and name
func GetObjectGivenGVKAndName(ctx context.Context, client client.Reader,
	gvk schema.GroupVersionKind, namespace, name string) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	apiVersion := metav1.GroupVersion{
		Group:   gvk.Group,
		Version: gvk.Version,
	}.String()
	obj.SetAPIVersion(apiVersion)
	obj.SetKind(gvk.Kind)
	err := client.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name},
		obj)
	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to get obj %s with gvk %+v ", name, gvk))
	}
	return obj, nil
}

// Object2Unstructured converts an object to an unstructured struct
func Object2Unstructured(obj interface{}) (*unstructured.Unstructured, error) {
	objMap, err := Object2Map(obj)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{
		Object: objMap,
	}, nil
}

// RawExtension2Unstructured converts a rawExtension to an unstructured struct
func RawExtension2Unstructured(raw *runtime.RawExtension) (*unstructured.Unstructured, error) {
	var objMap map[string]interface{}
	err := json.Unmarshal(raw.Raw, &objMap)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{
		Object: objMap,
	}, nil
}

// RawExtension2AppConfig converts runtime.RawExtention to ApplicationConfiguration
func RawExtension2AppConfig(raw runtime.RawExtension) (*v1alpha2.ApplicationConfiguration, error) {
	ac := &v1alpha2.ApplicationConfiguration{}
	b, err := raw.MarshalJSON()
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, ac); err != nil {
		return nil, err
	}
	return ac, nil
}

// Object2Map turn the Object to a map
func Object2Map(obj interface{}) (map[string]interface{}, error) {
	var res map[string]interface{}
	bts, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(bts, &res)
	return res, err
}

// Object2RawExtension converts an object to a rawExtension
func Object2RawExtension(obj interface{}) runtime.RawExtension {
	bts, _ := json.Marshal(obj)
	return runtime.RawExtension{
		Raw: bts,
	}
}

// RawExtension2Map will convert rawExtension to map
func RawExtension2Map(raw *runtime.RawExtension) (map[string]interface{}, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := raw.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var ret map[string]interface{}
	err = json.Unmarshal(data, &ret)
	if err != nil {
		return nil, err
	}
	return ret, err
}

// GenTraitName generate trait name
func GenTraitName(componentName string, ct *v1alpha2.ComponentTrait, traitType string) string {
	var traitMiddleName = TraitPrefixKey
	if traitType != "" && traitType != Dummy {
		traitMiddleName = strings.ToLower(traitType)
	}
	return fmt.Sprintf("%s-%s-%s", componentName, traitMiddleName, ComputeHash(ct))

}

// ComputeHash returns a hash value calculated from pod template and
// a collisionCount to avoid hash collision. The hash will be safe encoded to
// avoid bad words.
func ComputeHash(trait *v1alpha2.ComponentTrait) string {
	componentTraitHasher := fnv.New32a()
	DeepHashObject(componentTraitHasher, *trait)

	return rand.SafeEncodeString(fmt.Sprint(componentTraitHasher.Sum32()))
}

// DeepHashObject writes specified object to hash using the spew library
// which follows pointers and prints actual values of the nested objects
// ensuring the hash does not change when a pointer changes.
func DeepHashObject(hasher hash.Hash, objectToWrite interface{}) {
	hasher.Reset()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	_, _ = printer.Fprintf(hasher, "%#v", objectToWrite)
}

// GetComponent will get Component and RevisionName by AppConfigComponent
func GetComponent(ctx context.Context, client client.Reader, acc v1alpha2.ApplicationConfigurationComponent,
	namespace string) (*v1alpha2.Component, string, error) {
	c := &v1alpha2.Component{}
	var revisionName string
	if acc.RevisionName != "" {
		revision := &appsv1.ControllerRevision{}
		if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: acc.RevisionName}, revision); err != nil {
			return nil, "", errors.Wrapf(err, errFmtGetComponentRevision, acc.RevisionName)
		}
		c, err := UnpackRevisionData(revision)
		if err != nil {
			return nil, "", errors.Wrapf(err, errFmtControllerRevisionData, acc.RevisionName)
		}
		revisionName = acc.RevisionName
		return c, revisionName, nil
	}
	nn := types.NamespacedName{Namespace: namespace, Name: acc.ComponentName}
	if err := client.Get(ctx, nn, c); err != nil {
		return nil, "", errors.Wrapf(err, errFmtGetComponent, acc.ComponentName)
	}
	if c.Status.LatestRevision != nil {
		revisionName = c.Status.LatestRevision.Name
	}
	return c, revisionName, nil
}

// UnpackRevisionData will unpack revision.Data to Component
func UnpackRevisionData(rev *appsv1.ControllerRevision) (*v1alpha2.Component, error) {
	var err error
	if rev.Data.Object != nil {
		comp, ok := rev.Data.Object.(*v1alpha2.Component)
		if !ok {
			return nil, fmt.Errorf(errFmtInvalidRevisionType, rev.Name, reflect.TypeOf(rev.Data.Object))
		}
		return comp, nil
	}
	var comp v1alpha2.Component
	err = json.Unmarshal(rev.Data.Raw, &comp)
	return &comp, err
}

// AddLabels will merge labels with existing labels. If any conflict keys, use new value to override existing value.
func AddLabels(o labelAnnotationObject, labels map[string]string) {
	o.SetLabels(MergeMapOverrideWithDst(o.GetLabels(), labels))
}

// AddAnnotations will merge annotations with existing ones. If any conflict keys, use new value to override existing value.
func AddAnnotations(o labelAnnotationObject, annos map[string]string) {
	o.SetAnnotations(MergeMapOverrideWithDst(o.GetAnnotations(), annos))
}

// MergeMapOverrideWithDst merges two could be nil maps. Keep the dst for any conflicts,
func MergeMapOverrideWithDst(src, dst map[string]string) map[string]string {
	if src == nil && dst == nil {
		return nil
	}
	r := make(map[string]string)
	for k, v := range src {
		r[k] = v
	}
	// override the src for the same key
	for k, v := range dst {
		r[k] = v
	}
	return r
}

// ConvertComponentDef2WorkloadDef help convert a ComponentDefinition to WorkloadDefinition
func ConvertComponentDef2WorkloadDef(dm discoverymapper.DiscoveryMapper, componentDef *v1beta1.ComponentDefinition,
	workloadDef *v1beta1.WorkloadDefinition) error {
	var reference common.DefinitionReference
	reference, err := ConvertWorkloadGVK2Definition(dm, componentDef.Spec.Workload.Definition)
	if err != nil {
		return fmt.Errorf("create DefinitionReference fail %w", err)
	}

	workloadDef.SetName(componentDef.Name)
	workloadDef.SetNamespace(componentDef.Namespace)
	workloadDef.SetLabels(componentDef.Labels)
	workloadDef.SetAnnotations(componentDef.Annotations)
	workloadDef.Spec.Reference = reference
	workloadDef.Spec.ChildResourceKinds = componentDef.Spec.ChildResourceKinds
	workloadDef.Spec.Extension = componentDef.Spec.Extension
	workloadDef.Spec.RevisionLabel = componentDef.Spec.RevisionLabel
	workloadDef.Spec.Status = componentDef.Spec.Status
	workloadDef.Spec.Schematic = componentDef.Spec.Schematic
	return nil
}

// ExtractRevisionNum  extract revision number
func ExtractRevisionNum(appRevision string, delimiter string) (int, error) {
	splits := strings.Split(appRevision, delimiter)
	// check some bad appRevision name, eg:v1, appv2
	if len(splits) == 1 {
		return 0, ErrBadRevisionName
	}
	// check some bad appRevision name, eg:myapp-a1
	if !strings.HasPrefix(splits[len(splits)-1], "v") {
		return 0, ErrBadRevisionName
	}
	return strconv.Atoi(strings.TrimPrefix(splits[len(splits)-1], "v"))
}

// Min for int
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max for int
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Abs for int
func Abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

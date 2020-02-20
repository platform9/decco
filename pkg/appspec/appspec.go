// package appsec contains the App-related types (deprecated; use github.com/decco/api)
package appspec

import (
	deccov1 "github.com/platform9/decco/api/v1beta2"
)

const (
	CRDResourcePlural = "apps"
)

type (
	App          = deccov1.App
	AppSpec      = deccov1.AppSpec
	AppStatus    = deccov1.AppStatus
	AppList      = deccov1.AppList
	TlsEgress    = deccov1.TlsEgress
	EndpointSpec = deccov1.EndpointSpec
	AppCondition = deccov1.AppCondition
)

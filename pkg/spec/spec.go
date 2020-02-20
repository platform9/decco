// package spec contains the Space-related types (deprecated; use github.com/decco/api)
package spec

import (
	deccov1 "github.com/platform9/decco/api/v1beta2"
)

const (
	CRDResourcePlural = "spaces"
)

type (
	Space       = deccov1.Space
	SpaceSpec   = deccov1.SpaceSpec
	SpaceStatus = deccov1.SpaceStatus
	SpaceList   = deccov1.SpaceList
)

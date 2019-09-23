package v1beta3

// SpacePhase is a string representation of a Space Phase.
//
// This type is a high-level indicator of the status of the Space as it is created,
// from the API user’s perspective.
//
// TODO: Decide if below is still applicable to how we want to use SpacePhases
// The value should not be interpreted by any software components as a reliable indication
// of the actual state of the Space, and controllers should not use the Space Phase field
// value when making decisions about what action to take.
//
// Controllers should always look at the actual state of the Space’s fields to make those decisions.
type SpacePhase string

const (
	// SpacePhasePending is the first state a Space is assigned by
	// Decco Space controller after being created.
	SpacePhasePending = SpacePhase("pending")

	// SpacePhaseProvisioning is the state when the
	// Space infrastructure (DNS, etc) is being created.
	SpacePhaseProvisioning = SpacePhase("provisioning")

	// SpacePhaseProvisioned is the state when its
	// infrastructure has been created and configured.
	SpacePhaseProvisioned = SpacePhase("provisioned")

	// SpacePhaseDeploying is the Space state when it has
	// started deploying Apps from the configured Manifest
	SpacePhaseDeploying = SpacePhase("deploying")

	// SpacePhaseActive is the Space state when it has
	// finished deploying Apps from the configured Manifest
	// and is ready to use
	SpacePhaseActive = SpacePhase("active")

	// SpacePhaseDeleting is the Space state when a delete
	// request has been sent to the API Server,
	// but its infrastructure has not yet been fully deleted.
	SpacePhaseDeleting = SpacePhase("deleting")

	// SpacePhaseDeleted is the Space state when the object
	// and the related infrastructure is deleted and
	// ready to be garbage collected by the API Server.
	SpacePhaseDeleted = SpacePhase("deleted")

	// SpacePhaseFailed is the Space state when the system
	// might require user intervention.
	SpacePhaseFailed = SpacePhase("failed")

	// SpacePhaseUnknown is returned if the Space state cannot be determined.
	SpacePhaseUnknown = SpacePhase("")
)

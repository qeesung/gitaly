package featureflag

// CherryPickStructuredErrors enables the UserCherryPick RPC to return
// structured errors.
var CherryPickStructuredErrors = NewFeatureFlag("cherry_pick_structured_errors", false)

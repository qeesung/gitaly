package featureflag

// UploadPackHideRefs causes git-upload-pack(1) to hide internal references.
var UploadPackHideRefs = NewFeatureFlag(
	"upload_pack_hide_refs",
	"v15.3.0",
	"https://gitlab.com/gitlab-org/gitaly/-/issues/4390",
	false,
)

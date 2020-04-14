package git

// UploadPackFilterConfig confines config options that are required to allow
// partial-clone filters.
func UploadPackFilterConfig() []Option {
	return []Option{
		ValueFlag{"-c", "uploadpack.allowFilter=true"},
		ValueFlag{"-c", "uploadpack.allowAnySHA1InWant=true"},
	}
}

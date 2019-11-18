package featureflag

const (
	// UploadPackFilter enables partial clones by sending uploadpack.allowFilter and uploadpack.allowAnySHA1InWant
	// to upload-pack
	UploadPackFilter = "upload_pack_filter"
	// GetAllLFSPointersGo will cause the GetAllLFSPointers RPC to use the go implementation when set
	GetAllLFSPointersGo = "get_all_lfs_pointers_go"
	// GetTagMessagesGo will cause the GetTagMessages RPC to use the go implementation when set
	GetTagMessagesGo = "get_tag_messages_go"
	// LinguistFileCountStats will invoke an additional git-linguist command to get the number of files per language
	LinguistFileCountStats = "linguist_file_count_stats"
)

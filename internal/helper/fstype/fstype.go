package fstype

const unknownFS = "unknown"

// FileSystem will return the type of filesystem being used at the passed path
func FileSystem(path string) string {
	return detectFileSystem(path)
}

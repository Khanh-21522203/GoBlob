package util

import "path"

// FullPath is an absolute filer path like "/foo/bar/baz.txt".
type FullPath string

// Dir returns the parent directory path, e.g., "/foo/bar".
func (p FullPath) Dir() FullPath {
	dir := path.Dir(string(p))
	if dir == "." {
		return "/"
	}
	return FullPath(dir)
}

// Name returns the base filename, e.g., "baz.txt".
func (p FullPath) Name() string {
	return path.Base(string(p))
}

// Child appends a name to the path, e.g., p.Child("x") = "/foo/bar/x".
func (p FullPath) Child(name string) FullPath {
	return FullPath(path.Join(string(p), name))
}

// IsRoot returns true if this is the root path "/".
func (p FullPath) IsRoot() bool {
	return p == "/"
}

// AsString returns the path as a string.
func (p FullPath) AsString() string {
	return string(p)
}

// NewFullPath creates a FullPath from a string, ensuring it starts with "/".
func NewFullPath(s string) FullPath {
	if s == "" {
		return "/"
	}
	if s[0] != '/' {
		s = "/" + s
	}
	return FullPath(s)
}

// Parent returns the parent directory path (alias for Dir).
func (p FullPath) Parent() FullPath {
	return p.Dir()
}

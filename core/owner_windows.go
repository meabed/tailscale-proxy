package core

import "os"

// ownedByMe: the per-user profile folder already keeps ~/.tsp private on Windows.
func ownedByMe(os.FileInfo) bool { return true }

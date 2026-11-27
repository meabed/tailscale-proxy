package core

import "os"

// ownedByMe: the per-user profile folder already keeps ~/.tsp private on Windows.
func ownedByMe(os.FileInfo) bool { return true }

// othersCanWrite: Windows guards a user's files with ACLs, not mode bits.
func othersCanWrite(os.FileInfo) bool { return false }

// worldWritable: Windows has no world-writable folder bit (see othersCanWrite).
func worldWritable(os.FileInfo) bool { return false }

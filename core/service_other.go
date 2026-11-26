//go:build !darwin && !linux

package core

func platformService() (serviceSystem, bool) { return serviceSystem{}, false }

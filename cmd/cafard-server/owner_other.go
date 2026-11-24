//go:build !unix

package main

// sameOwner has nothing to do where files have no Unix owner.
func sameOwner(string, string) error { return nil }

// giveBack has nothing to do where files have no Unix owner.
func giveBack(string) {}

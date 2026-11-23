//go:build !unix

package main

// giveBack has nothing to do where files have no Unix owner.
func giveBack(string) {}

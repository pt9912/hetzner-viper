// Package filelock is an exclusive lock on a file, shared by the processes of one computer: the commands that must not
// run twice at once on the same thing (the ssh config, a compose project, the registry of a k3d cluster) take it.
package filelock


//go:build !windows && webui

package main

func openBrowserNative(url string) bool { return false }

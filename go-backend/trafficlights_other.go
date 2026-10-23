//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// alignTrafficLights is a no-op off macOS: traffic light buttons only exist
// on Mac. It keeps main.go's event hooks (which reference the Mac titlebar
// events) compilable on Windows and Linux.
func alignTrafficLights(window *application.WebviewWindow) {}

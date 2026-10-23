//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

// positionTitlebarButtons sets the vertical center of the traffic-light row
// to topPoints below the top edge. The target is expressed in the WebView's
// content coordinates (bottom-left origin, points downward) and converted
// into whatever internal container AppKit owns the buttons in — the
// convertPoint:fromView: hop through close.superview copes with the flipped
// titlebar space for us. Each button keeps its horizontal position.
static void positionTitlebarButtons(void *nsWindow, double topPoints) {
	NSWindow *window = (__bridge NSWindow *)nsWindow;
	if (!window) {
		return;
	}

	NSButton *close = [window standardWindowButton:NSWindowCloseButton];
	if (!close || !close.superview || !window.contentView) {
		return;
	}

	NSView *content = window.contentView;
	NSPoint desired = [close.superview convertPoint:
		NSMakePoint(NSMidX(close.frame), NSHeight(content.bounds) - topPoints)
		fromView:content];

	NSUInteger buttons[] = {NSWindowCloseButton, NSWindowMiniaturizeButton, NSWindowZoomButton};

	for (NSUInteger i = 0; i < 3; i++) {
		NSButton *button = [window standardWindowButton:buttons[i]];
		if (!button || !button.superview) {
			continue;
		}

		NSRect frame = button.frame;
		frame.origin.y = desired.y - NSHeight(frame) / 2;
		[button setFrameOrigin:frame.origin];
	}
}
*/
import "C"

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Native traffic-light placement: with MacTitleBarHiddenInsetUnified AppKit
// centers the buttons on its unified titlebar row, which lands mid-screen in
// the two-row top bar (RepoTabs row above, h-14 Toolbar below) — the renderer
// header wants them up with the tabs. trafficLightsTopInset is that row's
// vertical center: a 32pt band anchored to the window top, for the 15pt
// buttons.
const trafficLightsTopInset = 16

// alignTrafficLights repositions the window buttons onto the renderer's
// RepoTabs row. Safe to repeat: AppKit can re-layout the titlebar on focus
// and fullscreen transitions, so the re-apply hooks in main.go call this
// idempotently.
func alignTrafficLights(window *application.WebviewWindow) {
	application.InvokeSync(func() {
		C.positionTitlebarButtons(window.NativeWindow(), C.double(trafficLightsTopInset))
	})
}

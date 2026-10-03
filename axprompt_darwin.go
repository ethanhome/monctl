//go:build darwin

package main

// #cgo LDFLAGS: -framework ApplicationServices
// #import <ApplicationServices/ApplicationServices.h>
//
// // axPromptTrust shows the OS prompt (System Settings, app highlighted)
// // when untrusted and reports the current trust state. The hotkey lib's
// // registerTap only preflights with AXIsProcessTrusted(), so without this
// // call watch fails silently until the user finds the pane by hand.
// static int axPromptTrust(void) {
// 	const void *keys[] = { kAXTrustedCheckOptionPrompt };
// 	const void *vals[] = { kCFBooleanTrue };
// 	CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1, NULL, NULL);
// 	int trusted = AXIsProcessTrustedWithOptions(opts) ? 1 : 0;
// 	CFRelease(opts);
// 	return trusted;
// }
import "C"

// ensureAXTrust shows the Accessibility/Input Monitoring grant prompt when
// missing (the system coalesces to one alert per app) and reports trust.
func ensureAXTrust() bool { return C.axPromptTrust() != 0 }

package tui

const (
	iconApp         = "󰁹"
	iconHistory     = "󰋚"
	iconHealth      = "󰓙"
	iconBattery     = "󰁹"
	iconCharging    = "󰂄"
	iconPlugged     = "󰚥"
	iconUnplugged   = "󰚦"
	iconFull        = "󰁹"
	iconSleep       = "󰒲"
	iconResume      = "󰑐"
	iconPowerSaver  = "󰌪"
	iconBalanced    = "󰾅"
	iconPerformance = "󰓅"
	iconUnknown     = "󰋗"
	iconRefresh     = "󰑐"
	iconQuit        = "󰗼"
)

func profileIcon(profile string) string {
	switch profile {
	case "power-saver":
		return iconPowerSaver
	case "balanced":
		return iconBalanced
	case "performance":
		return iconPerformance
	default:
		return iconUnknown
	}
}

func stateIcon(state string) string {
	switch state {
	case "charging":
		return iconCharging
	case "full":
		return iconFull
	default:
		return iconBattery
	}
}

func eventIcon(eventType string) string {
	switch eventType {
	case "plugged":
		return iconPlugged
	case "unplugged":
		return iconUnplugged
	case "full":
		return iconFull
	case "sleep":
		return iconSleep
	case "resume":
		return iconResume
	default:
		return iconUnknown
	}
}

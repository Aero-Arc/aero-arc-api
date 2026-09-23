package preflight

import "testing"

func TestStaticEnvironmentCheckerKeepsDemoClears(t *testing.T) {
	builder := evaluateChecker(t, StaticEnvironmentChecker{}, testSnapshot(timeNow()))
	wx := requireCheck(t, builder, "demo_weather", "WX-DEMO", "demo_weather_provider", false)
	notam := requireCheck(t, builder, "demo_notam", "NOTAM-DEMO", "demo_notam_provider", false)
	if wx.Summary != "demo weather check clear" || notam.Summary != "demo NOTAM check clear" {
		t.Fatalf("summaries = %q, %q", wx.Summary, notam.Summary)
	}
	if builder.Checks()[0].ID != wx.ID || builder.Checks()[1].ID != notam.ID {
		t.Fatalf("order = %#v", builder.Checks())
	}
}

//go:build linux && amd64

package shellinstaller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUserContext7ProfileReachesReviewAndCanonicalStage(t *testing.T) {
	for _, choice := range []string{"on", "off"} {
		t.Run(choice, func(t *testing.T) {
			parent := t.TempDir()
			profile := DefaultUserExperience()
			profile.Context7, profile.CodeGraph = choice, "on"
			before := profile
			target := filepath.Join(parent, "selected")
			req := UserInstallRequest{Destination: target, Mode: "separate", Experience: &profile}
			token, err := InspectUserInstall(req)
			if err != nil || token == "" {
				t.Fatal("Context7 cannot reach physical review", token, err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("inspection wrote or acquired before consent", entries, err)
			}
			if err := userPrepareInstallStage(context.Background(), target, req.Mode, &profile); err != nil {
				t.Fatal(err)
			}
			assertExperienceFile(t, filepath.Join(target, "agent/mcp.json"), fmt.Sprintf("{\n  \"mcpServers\": {\n    \"context7\": {\n      \"url\": \"https://mcp.context7.com/mcp\",\n      \"enabled\": %t,\n      \"exposure\": \"codemode\"\n    }\n  }\n}\n", choice == "on"))
			if profile != before {
				t.Fatal("preparer modified the physical selection")
			}
			if _, err := os.Lstat(filepath.Join(target, "config/mcp.json")); !os.IsNotExist(err) {
				t.Fatal("Context7 staged in unused config scope", err)
			}
			if token, err := InspectUserInstall(req); err == nil || token != "" {
				t.Fatal("explicit Context7 may replace existing preferences")
			}
		})
	}
}

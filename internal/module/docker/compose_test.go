package docker

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func TestComposeDeployRejectsPathAndArgumentInjection(t *testing.T) {
	module := &Module{}
	router := gin.New()
	router.POST("/compose/:id/deploy", module.deployComposeTemplate)
	for _, name := range []string{"../other", "project/other", "--help", "other;command", "$(command)"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/compose/template/deploy", strings.NewReader(`{"project":"`+name+`"}`))
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("project %q returned HTTP %d", name, response.Code)
		}
	}
}

func TestComposeProjectFileRemovalFlag(t *testing.T) {
	for _, owned := range []bool{false, true} {
		data, err := json.Marshal(composeView(store.DockerComposeProject{Source: composeManaged, OwnedDir: owned}))
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			CanRemoveFiles *bool `json:"canRemoveFiles"`
		}
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		if response.CanRemoveFiles == nil || *response.CanRemoveFiles != owned {
			t.Fatalf("expected canRemoveFiles=%t in %s", owned, data)
		}
	}
}

func TestComposePreviewKeepsStagingSeparateFromProject(t *testing.T) {
	for _, existing := range []bool{false, true} {
		root := t.TempDir()
		work := filepath.Join(root, "project")
		if existing {
			if err := os.Mkdir(work, 0700); err != nil {
				t.Fatal(err)
			}
		}
		main := filepath.Join(work, "compose.yaml")
		env := filepath.Join(work, ".env")
		project := composeView(store.DockerComposeProject{Name: "project", WorkDir: work, ComposeFiles: []string{main}, EnvFile: env})
		dir := t.TempDir()
		yaml, dotenv := "services: {}", "KEY=value"
		staged, previewOnly, err := prepareComposePreview(project, composeFileChange{YAML: &yaml, Env: &dotenv}, dir)
		if err != nil {
			t.Fatal(err)
		}
		expectedWork := dir
		if existing {
			expectedWork = work
		}
		if previewOnly == existing || staged.WorkDir != expectedWork {
			t.Fatalf("existing=%t, workDir=%s, previewOnly=%t", existing, staged.WorkDir, previewOnly)
		}
		if filepath.Dir(staged.ComposeFiles[0]) != dir || filepath.Dir(staged.EnvFile) != dir || project.ComposeFiles[0] != main {
			t.Fatalf("unexpected staging paths: %v, %s", staged.ComposeFiles, staged.EnvFile)
		}
		if _, err := os.Stat(main); !os.IsNotExist(err) {
			t.Fatalf("preview touched the real file: %v", err)
		}
	}
}

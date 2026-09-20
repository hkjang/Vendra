package httpapi

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hkjang/Vendra/internal/config"
)

// The two guides are the product's only user-facing manuals, and both were
// written by reading the code and the running screens. Nothing kept them that
// way: three consecutive rounds compared the pictures and the environment
// variable table by hand, and each round since has changed both the guides and
// the configuration. These two tests do that comparison on every run.

const guidePictures = "docs/images/guide"

var (
	guideFiles = []string{"docs/USER_GUIDE.md", "docs/ADMIN_GUIDE.md"}
	// markdownImage matches ![alt](path); both guides only ever use paths
	// relative to docs/, such as images/guide/login.png.
	markdownImage = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	// getenvName is the one way the application reads its environment.
	getenvName = regexp.MustCompile(`os\.Getenv\("([A-Z_]+)"\)`)
	// tableCodeName is a table row whose first cell is a code-formatted name.
	tableCodeName = regexp.MustCompile("^\\|\\s*`([A-Z_]+)`\\s*\\|")
)

// TestEveryPictureTheGuidesShowExists holds the two guides and the picture
// directory to each other, in both directions.
//
// A picture renamed under a guide is a broken image in the PDF and the wiki;
// a picture nobody references is a screen that was captured, then dropped
// from the text, and will silently go stale in the repository.
func TestEveryPictureTheGuidesShowExists(t *testing.T) {
	referenced := map[string][]string{}
	for _, guide := range guideFiles {
		for _, m := range markdownImage.FindAllStringSubmatch(repoFile(t, guide), -1) {
			referenced[m[1]] = append(referenced[m[1]], guide)
		}
	}
	if len(referenced) < 10 {
		t.Fatalf("the guides reference %d pictures, so this comparison proves nothing", len(referenced))
	}
	for path, guides := range referenced {
		if _, err := os.Stat("../../docs/" + path); err != nil {
			t.Errorf("%s shows %s, which does not exist under docs/: %v", strings.Join(guides, " and "), path, err)
		}
	}

	entries, err := os.ReadDir("../../" + guidePictures)
	if err != nil {
		t.Fatalf("list %s: %v", guidePictures, err)
	}
	var orphans []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, shown := referenced["images/guide/"+entry.Name()]; !shown {
			orphans = append(orphans, entry.Name())
		}
	}
	if len(orphans) > 0 {
		t.Errorf("%s holds %d picture(s) that neither guide shows, so they will never be recaptured when the screen changes: %s",
			guidePictures, len(orphans), strings.Join(orphans, ", "))
	}
}

// TestAdminGuideNamesEveryEnvironmentVariableTheServerReads keeps the
// environment variable table in ADMIN_GUIDE.md equal to what internal/config
// reads, in both directions: a variable the server reads but the table omits
// is one an operator cannot know to set; a variable in the table the server
// no longer reads is one they will set for nothing.
//
// The table is found by its shape — the first four-column table with a
// 「필수」 column — rather than by the prose above it, which is the part of
// the guide most likely to be reworded. TZ, VENDRA_IMAGE, VENDRA_GUIDE_* and
// VENDRA_TEST_* are deliberately outside the comparison: the container, the
// compose file, the capture script and the test harnesses read those, not the
// application, and the guide lists the first two in a separate table.
func TestAdminGuideNamesEveryEnvironmentVariableTheServerReads(t *testing.T) {
	read := map[string]bool{}
	for _, m := range getenvName.FindAllStringSubmatch(repoFile(t, "internal/config/config.go"), -1) {
		read[m[1]] = true
	}
	if len(read) < 2 {
		t.Fatalf("internal/config reads %d environment variables, so this comparison proves nothing", len(read))
	}

	documented := requiredColumnTableNames(t, repoFile(t, "docs/ADMIN_GUIDE.md"))
	if len(documented) < 2 {
		t.Fatalf("the admin guide's environment variable table names %d variables, so this comparison proves nothing", len(documented))
	}

	for _, name := range sortedNames(read) {
		if !documented[name] {
			t.Errorf("the server reads %s, but the admin guide's environment variable table does not name it", name)
		}
	}
	for _, name := range sortedNames(documented) {
		if !read[name] {
			t.Errorf("the admin guide's environment variable table names %s, but the server never reads it", name)
		}
	}

	// The regexp says which names the source mentions; config.Load says which
	// ones the running binary refuses to start without. Every name the guide
	// marks 「필수」 has to be one Load complains about by name when it is the
	// only one missing.
	for _, name := range sortedNames(documented) {
		for other := range documented {
			if other == name {
				t.Setenv(other, "")
			} else {
				// Long enough that the bootstrap password does not log a warning.
				t.Setenv(other, strings.Repeat("x", 10))
			}
		}
		_, err := config.Load()
		if err == nil {
			t.Errorf("config.Load started without %s, which the guide marks as required", name)
		} else if !strings.Contains(err.Error(), name) {
			t.Errorf("config.Load refused to start without %s but its message does not say so: %v", name, err)
		}
	}
}

// requiredColumnTableNames returns the code-formatted names in the first
// column of the first four-column table that has a 「필수」 header cell.
func requiredColumnTableNames(t *testing.T, guide string) map[string]bool {
	t.Helper()
	lines := strings.Split(guide, "\n")
	for i := 0; i+1 < len(lines); i++ {
		header, separator := lines[i], strings.TrimSpace(lines[i+1])
		if !regexp.MustCompile(`^\|(\s*-+\s*\|){4}$`).MatchString(separator) {
			continue
		}
		hasRequired := false
		for _, cell := range strings.Split(header, "|") {
			if strings.TrimSpace(cell) == "필수" {
				hasRequired = true
			}
		}
		if !hasRequired {
			continue
		}
		names := map[string]bool{}
		for _, row := range lines[i+2:] {
			if !strings.HasPrefix(strings.TrimSpace(row), "|") {
				break
			}
			if m := tableCodeName.FindStringSubmatch(strings.TrimSpace(row)); m != nil {
				names[m[1]] = true
			}
		}
		return names
	}
	t.Fatal("the admin guide has no four-column table with a 필수 column, so the environment variable table cannot be found")
	return nil
}

func sortedNames(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

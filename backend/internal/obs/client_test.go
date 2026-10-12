package obs

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBasicAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<collection></collection>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	_, err := c.SearchProjects(context.Background(), "isv:percona")
	if err != nil {
		t.Fatal(err)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if gotAuth != expected {
		t.Errorf("auth header: got %q, want %q", gotAuth, expected)
	}
}

func TestSearchProjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<collection>
			<project name="isv:percona:ppg"/>
			<project name="isv:percona:pmm"/>
		</collection>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	projects, err := c.SearchProjects(context.Background(), "isv:percona")
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(projects), projects)
	}
	if projects[0] != "isv:percona:ppg" || projects[1] != "isv:percona:pmm" {
		t.Errorf("unexpected projects: %v", projects)
	}
}

func TestNon200Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Unauthorized", 401)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	_, err := c.SearchProjects(context.Background(), "isv:percona")
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestPackageBlockedReasons(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("package") != "mypkg" || r.URL.Query().Get("view") != "status" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<resultlist>
			<result project="isv:percona:ppg:17" repository="openSUSE_Tumbleweed" arch="x86_64" state="building">
				<status package="mypkg" code="blocked">
					<details>libfoo is not yet built</details>
				</status>
			</result>
			<result project="isv:percona:ppg:17" repository="openSUSE_Tumbleweed" arch="aarch64" state="building">
				<status package="mypkg" code="succeeded"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reasons, err := c.PackageBlockedReasons(context.Background(), "isv:percona:ppg:17", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if reasons["openSUSE_Tumbleweed/x86_64"] != "libfoo is not yet built" {
		t.Errorf("expected blocked reason for x86_64, got %q", reasons["openSUSE_Tumbleweed/x86_64"])
	}
	if _, ok := reasons["openSUSE_Tumbleweed/aarch64"]; ok {
		t.Error("succeeded target should not appear in reasons map")
	}
}

func TestPackageBlockedReasonsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<resultlist>
			<result project="isv:percona:ppg:17" repository="openSUSE_Tumbleweed" arch="x86_64" state="building">
				<status package="mypkg" code="blocked"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	reasons, err := c.PackageBlockedReasons(context.Background(), "isv:percona:ppg:17", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	// blocked with no <details> → omitted from map
	if len(reasons) != 0 {
		t.Errorf("expected empty map, got %v", reasons)
	}
}

func TestPackageBuildResults(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/build/isv:percona/_result" && r.URL.Query().Get("package") == "mypkg" {
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<resultlist>
			  <result project="isv:percona" repository="openSUSE_Tumbleweed" arch="x86_64" state="building">
				<status package="mypkg" code="building"/>
			  </result>
			  <result project="isv:percona" repository="openSUSE_Tumbleweed" arch="aarch64" state="failed">
				<status package="mypkg" code="failed"/>
			  </result>
			</resultlist>`))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "user", "pass")
	results, err := c.PackageBuildResults(context.Background(), "isv:percona", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	found := map[string]string{}
	for _, r := range results {
		found[r.Arch] = r.State
	}
	if found["x86_64"] != "building" {
		t.Errorf("x86_64 expected building, got %s", found["x86_64"])
	}
	if found["aarch64"] != "failed" {
		t.Errorf("aarch64 expected failed, got %s", found["aarch64"])
	}
}

func TestPackageBuildReason(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/build/isv:percona/openSUSE_Tumbleweed/x86_64/mypkg/_reason" {
			w.Header().Set("Content-Type", "application/xml")
			w.Write([]byte(`<reason>
			  <explain>meta change</explain>
			  <time>1234567890</time>
			  <packagechange change="md5sum" key="libfoo"/>
			  <packagechange change="md5sum" key="libbar"/>
			</reason>`))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "user", "pass")
	res, err := c.PackageBuildReason(context.Background(), "isv:percona", "openSUSE_Tumbleweed", "x86_64", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if res.Explain != "meta change" {
		t.Errorf("expected 'meta change', got %q", res.Explain)
	}
	if len(res.Packages) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(res.Packages))
	}
	if res.Packages[0] != "libfoo" || res.Packages[1] != "libbar" {
		t.Errorf("unexpected packages: %v", res.Packages)
	}
}

func TestPackageBuildReasonNonMeta(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<reason><explain>source change</explain></reason>`))
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "user", "pass")
	res, err := c.PackageBuildReason(context.Background(), "isv:percona", "repo", "arch", "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if res.Explain != "source change" {
		t.Errorf("expected 'source change', got %q", res.Explain)
	}
	if len(res.Packages) != 0 {
		t.Errorf("expected no packages for non-meta reason, got %v", res.Packages)
	}
}

func TestRepoPublishStates(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("package") != "mypkg" || r.URL.Query().Get("view") != "status" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<resultlist>
			<result repository="Ubuntu_24.04" arch="x86_64" state="published">
				<status package="mypkg" code="succeeded"/>
			</result>
			<result repository="Ubuntu_24.04" arch="aarch64" state="building">
				<status package="mypkg" code="building"/>
			</result>
		</resultlist>`))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "u", "p")
	states, err := c.RepoPublishStates(context.Background(), "isv:percona", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if states["Ubuntu_24.04/x86_64"] != "published" {
		t.Errorf("expected published for Ubuntu_24.04/x86_64, got %q", states["Ubuntu_24.04/x86_64"])
	}
	if states["Ubuntu_24.04/aarch64"] != "building" {
		t.Errorf("expected building for Ubuntu_24.04/aarch64, got %q", states["Ubuntu_24.04/aarch64"])
	}
}

func TestPackageIsContainer(t *testing.T) {
	tests := []struct {
		name     string
		xml      string
		expected bool
	}{
		{
			name:     "dockerfile",
			xml:      `<sourceinfo><filename>Dockerfile</filename><filename>config.sh</filename></sourceinfo>`,
			expected: true,
		},
		{
			name:     "kiwi",
			xml:      `<sourceinfo><filename>percona-distribution-postgresql.kiwi</filename></sourceinfo>`,
			expected: true,
		},
		{
			name:     "rpm spec",
			xml:      `<sourceinfo><filename>percona-pg_tde.spec</filename></sourceinfo>`,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.xml))
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "u", "p")
			got, err := c.PackageIsContainer(context.Background(), "isv:percona:ppg:17", "mypkg")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.expected {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestPackageIsContainerNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "p")
	got, err := c.PackageIsContainer(context.Background(), "isv:percona:ppg:17", "percona-patroni")
	if err != nil {
		t.Fatalf("expected nil error for 404, got: %v", err)
	}
	if got {
		t.Error("expected false for package with no images repository")
	}
}

func TestPackageVersionResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("view") != "versrel" {
			http.Error(w, "bad view", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`<resultlist>
			<result repository="UBI_9" arch="x86_64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-1"/>
			</result>
			<result repository="UBI_9" arch="aarch64" state="published">
				<status package="percona-pg_tde" code="succeeded" versrel="17.5-1"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versrel, err := c.PackageVersionResult(context.Background(), "isv:percona:ppg:17", "percona-pg_tde")
	if err != nil {
		t.Fatal(err)
	}
	if versrel != "17.5-1" {
		t.Errorf("expected %q, got %q", "17.5-1", versrel)
	}
}

func TestPackageVersionResultEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<resultlist>
			<result repository="UBI_9" arch="x86_64" state="building">
				<status package="mypkg" code="building"/>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versrel, err := c.PackageVersionResult(context.Background(), "isv:percona:ppg:17", "mypkg")
	if err != nil {
		t.Fatal(err)
	}
	if versrel != "" {
		t.Errorf("expected empty string for unbuilt package, got %q", versrel)
	}
}

func TestPackageContainerInfoFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<binarylist>
			<binary filename="percona-distribution-postgresql-18.4-1.x86_64-1.7.tar" size="305101312" mtime="1781559533"/>
			<binary filename="percona-distribution-postgresql.x86_64-1.7.containerinfo" size="1234" mtime="1781559533"/>
		</binarylist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	filename, err := c.PackageContainerInfoFilename(context.Background(),
		"isv:percona:ppg:17:containers:ubi8", "images", "x86_64", "percona-distribution-postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if filename != "percona-distribution-postgresql.x86_64-1.7.containerinfo" {
		t.Errorf("unexpected filename: %q", filename)
	}
}

func TestProjectBinaryListParsesMTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<resultlist>
			<result project="isv:percona:ppg:releases:17" repository="openSUSE_Tumbleweed" arch="x86_64">
				<binarylist package="etcd">
					<binary filename="etcd-3.5.30-2.1.x86_64.rpm" size="18544085" mtime="1779201973"/>
				</binarylist>
			</result>
		</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	items, err := c.ProjectBinaryList(context.Background(), "isv:percona:ppg:releases:17")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Package != "etcd" {
		t.Errorf("Package = %q", items[0].Package)
	}
	if items[0].MTime != 1779201973 {
		t.Errorf("MTime = %d", items[0].MTime)
	}
	if got := items[0].BuiltAt.Unix(); got != 1779201973 {
		t.Errorf("BuiltAt unix = %d", got)
	}
}

func TestPackageContainerInfoFilenameAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<binarylist></binarylist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	filename, err := c.PackageContainerInfoFilename(context.Background(), "proj", "repo", "arch", "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if filename != "" {
		t.Errorf("expected empty, got %q", filename)
	}
}

func TestPackageContainerTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{
			"version": "18.4-1",
			"tags": [
				"percona-distribution-postgresql:18.4-1-1.7",
				"percona-distribution-postgresql:18.4-1",
				"percona-distribution-postgresql:18.4",
				"percona-distribution-postgresql:18"
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	tags, err := c.PackageContainerTags(context.Background(),
		"proj", "images", "x86_64", "pkg", "pkg.containerinfo")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 4 {
		t.Fatalf("expected 4 tags, got %d: %v", len(tags), tags)
	}
	if tags[0] != "18.4-1-1.7" {
		t.Errorf("expected tags[0] %q, got %q", "18.4-1-1.7", tags[0])
	}
}

func TestPackageContainerTagsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tags": []}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	tags, err := c.PackageContainerTags(context.Background(), "proj", "repo", "arch", "pkg", "file.containerinfo")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 0 {
		t.Errorf("expected empty, got %v", tags)
	}
}

func TestRepoBinaryVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/build/isv:percona:ppg:releases:17/openSUSE_Tumbleweed/x86_64/_repository" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("view") != "binaryversions" || r.URL.Query().Get("withevr") != "1" {
			http.Error(w, "bad query", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<binaryversionlist>
			<binary name="etcd-3.5.30-2.1.x86_64.rpm" sizek="17985" evr="3.5.30-2.1" arch="x86_64"/>
			<binary name="etcd-debugsource-3.5.30-2.1.x86_64.rpm" sizek="100" evr="3.5.30-2.1" arch="x86_64"/>
		</binaryversionlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versions, err := c.RepoBinaryVersions(context.Background(),
		"isv:percona:ppg:releases:17", "openSUSE_Tumbleweed", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(versions), versions)
	}
	if versions["etcd-3.5.30-2.1.x86_64.rpm"] != "3.5.30-2.1" {
		t.Errorf("expected '3.5.30-2.1', got %q", versions["etcd-3.5.30-2.1.x86_64.rpm"])
	}
	if versions["etcd-debugsource-3.5.30-2.1.x86_64.rpm"] != "3.5.30-2.1" {
		t.Errorf("expected '3.5.30-2.1', got %q", versions["etcd-debugsource-3.5.30-2.1.x86_64.rpm"])
	}
}

func TestRepoBinaryVersionsStripsEpoch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<binaryversionlist>
			<binary name="postgresql16-16.4-2.3.x86_64.rpm" sizek="1234" evr="2:16.4-2.3" arch="x86_64"/>
		</binaryversionlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versions, err := c.RepoBinaryVersions(context.Background(), "proj", "repo", "x86_64")
	if err != nil {
		t.Fatal(err)
	}
	if versions["postgresql16-16.4-2.3.x86_64.rpm"] != "16.4-2.3" {
		t.Errorf("expected epoch stripped to '16.4-2.3', got %q",
			versions["postgresql16-16.4-2.3.x86_64.rpm"])
	}
}

func TestRepoBinaryVersionsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<binaryversionlist></binaryversionlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	versions, err := c.RepoBinaryVersions(context.Background(), "proj", "repo", "arch")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Errorf("expected empty map, got %v", versions)
	}
}

func TestRebuild_Success(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	err := c.Rebuild(context.Background(), "isv:percona:ppg:17", "RockyLinux_9", "x86_64", "percona-pg_tde")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if !strings.Contains(gotPath, "cmd=rebuild") {
		t.Errorf("expected cmd=rebuild in URL, got %s", gotPath)
	}
}

func TestRebuild_NonSuccessResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "permission denied", http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	err := c.Rebuild(context.Background(), "isv:percona:ppg:17", "RockyLinux_9", "x86_64", "percona-pg_tde")
	if err == nil {
		t.Fatal("expected error on 403, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 in error, got: %v", err)
	}
}

func TestRebuild_URLEncoding(t *testing.T) {
	var capturedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedURL = r.URL.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "user", "pass")
	_ = c.Rebuild(context.Background(), "isv:percona:ppg:17", "RockyLinux_9", "x86_64", "percona-pg_tde")

	// url.PathEscape does not encode colons (valid in URL path segments per RFC 3986),
	// so the project name appears unescaped in the path.
	if !strings.Contains(capturedURL, "/build/isv:percona:ppg:17") {
		t.Errorf("project not in URL path: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "repository=RockyLinux_9") {
		t.Errorf("repo missing from URL: %s", capturedURL)
	}
	if !strings.Contains(capturedURL, "package=percona-pg_tde") {
		t.Errorf("package missing from URL: %s", capturedURL)
	}
}

func TestPublishFlagsResolution(t *testing.T) {
	cases := []struct {
		name string
		meta string
		repo string
		want bool
	}{
		{"no publish block", `<project name="p"></project>`, "images", true},
		{"bare disable", `<project name="p"><publish><disable/></publish></project>`, "images", false},
		{"repo disable", `<project name="p"><publish><disable repository="UBI_8"/></publish></project>`, "UBI_8", false},
		{"repo disable other repo publishes", `<project name="p"><publish><disable repository="UBI_8"/></publish></project>`, "images", true},
		{"disable all, enable one", `<project name="p"><publish><disable/><enable repository="images"/></publish></project>`, "images", true},
		{"disable all, enable one, other stays disabled", `<project name="p"><publish><disable/><enable repository="images"/></publish></project>`, "UBI_8", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := parsePublishFlags([]byte(tc.meta))
			if err != nil {
				t.Fatalf("parsePublishFlags: %v", err)
			}
			if got := f.Publishes(tc.repo); got != tc.want {
				t.Fatalf("Publishes(%q) = %v, want %v", tc.repo, got, tc.want)
			}
		})
	}
}

func TestProjectPublishFlagsCacheAndEvict(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`<project name="p"><publish><disable/></publish></project>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()
	if _, err := c.ProjectPublishFlags(ctx, "isv:percona:common:containers:ubi8"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := c.ProjectPublishFlags(ctx, "isv:percona:common:containers:ubi8"); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("HTTP calls = %d, want 1 (cached)", got)
	}
	c.EvictPublishFlags("isv:percona:common:containers:ubi8")
	if _, err := c.ProjectPublishFlags(ctx, "isv:percona:common:containers:ubi8"); err != nil {
		t.Fatalf("third: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("HTTP calls = %d, want 2 (refetched after evict)", got)
	}
	if c.MetricsSnapshot()["publish_flags"] != 2 {
		t.Fatalf("publish_flags metric = %d, want 2", c.MetricsSnapshot()["publish_flags"])
	}
}

func TestClientMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<resultlist></resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	ctx := context.Background()
	if _, _, err := c.BuildResults(ctx, "isv:percona:ppg:17"); err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	if _, _, err := c.BuildResults(ctx, "isv:percona:ppg:17"); err != nil {
		t.Fatalf("BuildResults: %v", err)
	}
	snap := c.MetricsSnapshot()
	if snap["build_results"] != 2 {
		t.Fatalf("build_results = %d, want 2", snap["build_results"])
	}
	snap["build_results"] = 99
	if c.MetricsSnapshot()["build_results"] != 2 {
		t.Fatalf("snapshot is not a copy")
	}
}

func TestBuildResultsRepoStates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<resultlist state="x">
  <result project="p" repository="images" arch="x86_64" state="published">
    <status package="pkg-a" code="succeeded"/>
  </result>
  <result project="p" repository="images" arch="aarch64" state="building">
    <status package="pkg-a" code="building"/>
  </result>
</resultlist>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "u", "p")
	states, repoStates, err := c.BuildResults(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 {
		t.Fatalf("want 2 package states, got %d", len(states))
	}
	if states[0].Package != "pkg-a" || states[0].State != "succeeded" {
		t.Fatalf("unexpected first package state: %+v", states[0])
	}
	if got := repoStates["images/x86_64"]; got != "published" {
		t.Fatalf("images/x86_64 state = %q, want published", got)
	}
	if got := repoStates["images/aarch64"]; got != "building" {
		t.Fatalf("images/aarch64 state = %q, want building", got)
	}
}

func TestPackageAggregateSource(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		status   int
		wantProj string
		wantPkg  string
		wantOK   bool
	}{
		{"explicit package", `<aggregatelist><aggregate project="isv:percona:ppg:common:deps"><package>etcd</package></aggregate></aggregatelist>`, 200, "isv:percona:ppg:common:deps", "etcd", true},
		{"whole project defaults to same name", `<aggregatelist><aggregate project="isv:percona:ppg:common:deps"/></aggregatelist>`, 200, "isv:percona:ppg:common:deps", "etcd", true},
		{"several packages picks the matching name", `<aggregatelist><aggregate project="isv:percona:ppg:common:deps"><package>other</package><package>etcd</package></aggregate></aggregatelist>`, 200, "isv:percona:ppg:common:deps", "etcd", true},
		{"not an aggregate", ``, 404, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/source/isv:percona:ppg:staging:18/etcd/_aggregate" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "u", "p")
			proj, pkg, ok, err := c.PackageAggregateSource(context.Background(), "isv:percona:ppg:staging:18", "etcd")
			if err != nil {
				t.Fatal(err)
			}
			if proj != tc.wantProj || pkg != tc.wantPkg || ok != tc.wantOK {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)", proj, pkg, ok, tc.wantProj, tc.wantPkg, tc.wantOK)
			}
		})
	}
}

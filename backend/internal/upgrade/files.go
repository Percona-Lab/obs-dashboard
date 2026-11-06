package upgrade

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	requestFile = "apply-request"
	statusFile  = "apply-status"
)

// Request is the apply-request contract (spec §4). Fields may only be added.
type Request struct {
	Version     string `json:"version"`
	Previous    string `json:"previous"`
	Backup      string `json:"backup"`
	RequestedBy string `json:"requested_by"`
	RequestedAt string `json:"requested_at"`
}

// Status is the apply-status contract written by the host.
type Status struct {
	Version    string `json:"version"`
	Previous   string `json:"previous"`
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	FinishedAt string `json:"finished_at"`
}

// writeAtomic writes v as JSON to dir/name via temp file, fsync and rename.
func writeAtomic(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	ok = true
	return nil
}

// readStatus returns the host's apply-status. present is false when there is
// none; err is non-nil when the file exists but cannot be parsed.
func readStatus(dir string) (st Status, present bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, statusFile))
	if os.IsNotExist(err) {
		return Status{}, false, nil
	}
	if err != nil {
		return Status{}, true, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return Status{}, true, fmt.Errorf("parse %s: %w", statusFile, err)
	}
	return st, true, nil
}

// ValidateApplyDir checks that dir exists, is a directory and is writable.
func ValidateApplyDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("UPGRADE_APPLY_DIR %q: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("UPGRADE_APPLY_DIR %q is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".probe.*")
	if err != nil {
		return fmt.Errorf("UPGRADE_APPLY_DIR %q is not writable: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

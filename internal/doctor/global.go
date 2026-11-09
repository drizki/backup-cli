package doctor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drizki/backup-cli/internal/systemd"
)

// zfsBclone is the OpenZFS module parameter that enables block cloning.
const zfsBclone = "module/zfs/parameters/zfs_bclone_enabled"

func checkState(c *collector, env Env) {
	dir := env.Store.Dir
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.add(Warn, "state directory", "", "%s does not exist yet; the first add or run creates it", dir)
	case err != nil:
		c.add(Fail, "state directory", "", "%s: %v", dir, err)
	case !fi.IsDir():
		c.add(Fail, "state directory", "", "%s is not a directory", dir)
	case env.Access(dir, CanWrite) != nil:
		c.add(Fail, "state directory", "", "%s is not writable by this account; runs cannot be recorded", dir)
	default:
		c.add(OK, "state directory", "", "%s is writable", dir)
	}
}

func checkSystemd(c *collector, env Env) {
	if env.Manager == nil || !env.Manager.Available() {
		c.add(Warn, "systemd", "", "systemd is not available; scheduled backups need cron (see 'backup-cli help add')")
		return
	}
	c.add(OK, "systemd", "", "systemd is available")
}

// checkZFS warns when OpenZFS block cloning is enabled: copies may then share
// blocks below the filesystem, which defeats physically independent backups.
func checkZFS(c *collector, env Env) {
	b, err := os.ReadFile(filepath.Join(env.SysRoot, zfsBclone))
	if err != nil {
		c.add(OK, "zfs block cloning", "", "not applicable (no OpenZFS block cloning parameter)")
		return
	}
	switch v := strings.TrimSpace(string(b)); v {
	case "0":
		c.add(OK, "zfs block cloning", "", "zfs_bclone_enabled is 0; copies are physically separate")
	case "1":
		c.add(Warn, "zfs block cloning", "", "zfs_bclone_enabled is 1; copies may share blocks below the filesystem. Set it to 0 if you need physically separate backups")
	default:
		c.add(Warn, "zfs block cloning", "", "zfs_bclone_enabled has the unexpected value %q", v)
	}
}

// checkTimer reports whether the job's timer is active.
func checkTimer(c *collector, env Env, name string, scheduled bool) {
	if !scheduled {
		c.add(OK, "timer", name, "not scheduled (run it by hand or from cron)")
		return
	}
	if env.Manager == nil || !env.Manager.Available() {
		c.add(Warn, "timer", name, "scheduled, but systemd is not available")
		return
	}
	unit := systemd.UnitName(name)
	active, err := env.Manager.IsActive(unit)
	switch {
	case err != nil:
		c.add(Warn, "timer", name, "cannot query %s.timer: %v", unit, err)
	case !active:
		c.add(Warn, "timer", name, "%s.timer is not active; run 'systemctl enable --now %s.timer' or add the job again", unit, unit)
	default:
		c.add(OK, "timer", name, "%s.timer is active", unit)
	}
}

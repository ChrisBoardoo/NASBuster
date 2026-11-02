// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/buildinfo"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/sysexec"
	"github.com/crosswax/nasbuster/internal/validate"
)

// Ops executes the privileged operations.
type Ops struct {
	Run sysexec.Runner
	FS  fs.FS // system root, for /etc/hosts and zoneinfo checks
	// ByIDDir is "/dev/disk/by-id" in production.
	ByIDDir string
	// DeviceExists reports whether path resolves to a block device.
	DeviceExists func(path string) bool
	// WriteFile replaces a system file atomically.
	WriteFile func(path string, data []byte, perm fs.FileMode) error
	Logger    *slog.Logger

	// Discover is the helper's OWN view of the disks. Destructive operations
	// re-check every disk with it and never trust what the caller claims.
	Discover func(ctx context.Context) ([]storage.Disk, error)
	// Files performs the pool-related file changes.
	Files Files
	// UnitDir receives generated systemd units (/etc/systemd/system).
	UnitDir string
	// MDConf is mdadm's configuration file (/etc/mdadm/mdadm.conf).
	MDConf string
	// Sleep is used while waiting for devices (tests make it instant).
	Sleep func(time.Duration)
	// SMBConf is Samba's configuration file (/etc/samba/smb.conf).
	SMBConf string
	// Now names snapshots (tests and demo use a fake clock).
	Now func() time.Time
	// ReadDaemonExport reads the daemon's database copy for configuration
	// backups (nil: the fixed path, without following links).
	ReadDaemonExport func() ([]byte, error)
	// Chown gives the restored database to the daemon (nil in demo and tests).
	Chown func(path string, uid, gid int) error
	// WriteSysfs writes a kernel attribute in place (/sys/block/md127/md/sync_min):
	// never atomically, sysfs has no rename.
	WriteSysfs func(path string, value []byte) error

	busy poolBusy
	// initramfsMu keeps two refreshes from starting the same rebuild.
	initramfsMu sync.Mutex
	// mdConfMu serializes every read-modify-write of mdadm.conf: pool
	// operations hold their pool's lock only, and the initramfs refresh none.
	mdConfMu sync.Mutex
	// idsMu keeps two creations from picking the same account number (P3).
	idsMu sync.Mutex
}

// poolBusy lets one operation at a time change a pool's disks or array: a
// second click, or the scheduler, could stop what the first just started
// (two reassemblies at once, a repair during a takeover).
type poolBusy struct {
	mu   sync.Mutex
	held map[string]bool
}

// lockPool takes the pool, or reports that another operation holds it.
func (o *Ops) lockPool(name string) (unlock func(), err *Error) {
	b := &o.busy
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.held[name] {
		return nil, errorf(CodePoolBusy, "another operation on pool %s is under way", name)
	}
	if b.held == nil {
		b.held = map[string]bool{}
	}
	b.held[name] = true
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.held, name)
	}, nil
}

// NewOps returns production Ops over the real system. labDir enables lab
// mode in the helper itself: only loop devices backed by files in labDir are
// ever eligible, whatever nasbusterd asks.
func NewOps(logger *slog.Logger, labDir string) *Ops {
	runner := sysexec.OSRunner{}
	root := os.DirFS("/")
	discoverer := &storage.Discoverer{Run: runner, FS: root, LabDir: labDir}
	return &Ops{
		Run:          runner,
		FS:           root,
		ByIDDir:      "/dev/disk/by-id",
		DeviceExists: isBlockDevice,
		WriteFile:    atomicWrite,
		Logger:       logger,
		Discover:     discoverer.Disks,
		Files:        OSFiles{},
		Chown:        osChown,
		WriteSysfs:   writeSysfs,
		UnitDir:      "/etc/systemd/system",
		MDConf:       "/etc/mdadm/mdadm.conf",
		SMBConf:      "/etc/samba/smb.conf",
	}
}

func writeSysfs(p string, value []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(value)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// exclusive runs an operation that changes a pool's disks or array, alone
// on that pool (an invalid name is refused by the operation itself).
func (o *Ops) exclusive(pool string, op func() (any, *Error)) (any, *Error) {
	unlock, err := o.lockPool(pool)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return op()
}

func (o *Ops) logger() *slog.Logger {
	if o.Logger == nil {
		return slog.Default()
	}
	return o.Logger
}

// Dispatch runs one request. It is the single entry point for every
// privileged action: unknown operations and unknown fields are rejected.
func (o *Ops) Dispatch(ctx context.Context, req Request) (any, *Error) {
	switch req.Op {
	case OpPing:
		return PingResult{Pong: true, Version: buildinfo.Version}, nil
	case OpDiskSmart:
		var args DiskSmartArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.diskSmart(ctx, args)
	case OpSetHostname:
		var args SetHostnameArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.setHostname(ctx, args)
	case OpSetTimezone:
		var args SetTimezoneArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.setTimezone(ctx, args)
	case OpPoolCreate:
		var args PoolCreateArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		res, err := o.poolCreate(ctx, args)
		o.afterMDConfChange(ctx) // also after a failure: the rollback rewrites mdadm.conf
		return res, err
	case OpPoolStatus:
		var args PoolArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.poolStatus(ctx, args)
	case OpPoolScrub, OpPoolScrubCancel, OpPoolScrubResume:
		var args PoolArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		switch req.Op {
		case OpPoolScrubCancel:
			return o.poolScrubCancel(ctx, args)
		case OpPoolScrubResume:
			return o.poolScrubResume(ctx, args)
		}
		return o.poolScrub(ctx, args)
	case OpEngineStatus:
		return o.engineStatus(ctx), nil
	case OpAppsStatus:
		return o.appsStatus(ctx)
	case OpEngineInstall:
		var args EngineInstallArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.engineInstall(ctx, args)
	case OpAppJob:
		var args JobArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appJob(ctx, args)
	case OpAppDeploy:
		var args AppDeployArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appDeploy(ctx, args)
	case OpAppAction:
		var args AppActionArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appAction(ctx, args)
	case OpAppRemove:
		var args AppRemoveArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appRemove(ctx, args)
	case OpAppTrashPurge:
		var args TrashPurgeArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appTrashPurge(ctx, args)
	case OpAppOrphan:
		var args OrphanArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appOrphan(ctx, args)
	case OpAppUpdate:
		var args AppActionArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appUpdate(ctx, args)
	case OpEngineRemove:
		var args EngineRemoveArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.engineRemove(ctx, args)
	case OpAppLogs:
		var args AppLogsArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.appLogs(ctx, args)
	case OpDiskLocate:
		var args DiskSmartArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.diskLocate(ctx, args)
	case OpDiskWipe:
		var args DiskWipeArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.diskWipe(ctx, args)
	case OpPoolsScan:
		return o.poolsScan(ctx)
	case OpConfigBackup:
		var args BackupArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.configBackup(ctx, args)
	case OpConfigBackups:
		return o.configBackups(ctx)
	case OpImportSources:
		return o.importSources(ctx)
	case OpImportMount, OpImportUnmount:
		var args ImportMountArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		if req.Op == OpImportMount {
			return o.importMount(ctx, args)
		}
		return o.importUnmount(ctx, args)
	case OpImportCopy:
		var args ImportCopyArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.importCopy(ctx, args)
	case OpImportProgress:
		var args ImportShareArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.importProgress(ctx, args)
	case OpConfigRestore:
		var args RestoreArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.configRestore(ctx, args)
	case OpPoolMountDegraded, OpPoolDestroy, OpPoolImport:
		var args PoolConfirmArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		res, err := o.exclusive(args.Name, func() (any, *Error) {
			switch req.Op {
			case OpPoolDestroy:
				return o.poolDestroy(ctx, args)
			case OpPoolImport:
				return o.poolImport(ctx, args)
			}
			return o.poolMountDegraded(ctx, args)
		})
		if req.Op != OpPoolMountDegraded && (err == nil || err.Code != CodePoolBusy) {
			o.afterMDConfChange(ctx) // also after a failure (a rollback rewrites mdadm.conf), not after a refusal
		}
		return res, err
	case OpPoolReplace:
		var args PoolReplaceArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.exclusive(args.Name, func() (any, *Error) { return o.poolReplace(ctx, args) })
	case OpPoolResume:
		var args PoolResumeArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.exclusive(args.Name, func() (any, *Error) { return o.poolResume(ctx, args) })
	case OpPoolUnitsRefresh:
		return o.poolUnitsRefresh(ctx)
	case OpEngineRefresh:
		return o.engineConfigRefresh(ctx)
	case OpInitramfsRefresh:
		return o.initramfsRefresh(ctx)
	case OpJournalPersist:
		var args JournalPersistArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.journalPersist(ctx, args)
	case OpPoolReassemble:
		var args PoolReassembleArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.exclusive(args.Name, func() (any, *Error) { return o.poolReassemble(ctx, args) })
	case OpPoolRepairParity:
		var args PoolArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.exclusive(args.Name, func() (any, *Error) { return o.poolRepairParity(ctx, args) })
	case OpPoolRestore, OpPoolClearErrors:
		var args PoolArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		if req.Op == OpPoolRestore {
			return o.exclusive(args.Name, func() (any, *Error) { return o.poolRestoreRedundancy(ctx, args) })
		}
		return o.poolClearErrors(ctx, args)
	case OpUserCreate, OpUserSetPassword, OpUserDelete:
		var args UserArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		switch req.Op {
		case OpUserCreate:
			return o.userCreate(ctx, args)
		case OpUserSetPassword:
			return o.userSetPassword(ctx, args)
		default:
			return o.userDelete(ctx, args)
		}
	case OpRecyclePurge:
		var args RecyclePurgeArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.recyclePurge(ctx, args)
	case OpShareDelete:
		var args ShareCreateArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.shareDelete(ctx, args)
	case OpShareCreate:
		var args ShareCreateArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.shareCreate(ctx, args)
	case OpSMBApply:
		var args SMBApplyArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.smbApply(ctx, args)
	case OpSMBSessions:
		return o.smbSessions(ctx)
	case OpMachineAccountVerify:
		var args MachineAccountArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		return o.verifyMachineAccount(ctx, args)
	case OpSnapshotCreate, OpSnapshotList, OpSnapshotDelete:
		var args SnapshotArgs
		if err := decodeArgs(req.Args, &args); err != nil {
			return nil, err
		}
		switch req.Op {
		case OpSnapshotCreate:
			return o.snapshotCreate(ctx, args)
		case OpSnapshotList:
			return o.snapshotList(ctx, args)
		default:
			return o.snapshotDelete(ctx, args)
		}
	default:
		return nil, errorf(CodeUnknownOp, "unknown operation %q", req.Op)
	}
}

func decodeArgs(raw json.RawMessage, v any) *Error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errorf(CodeInvalidArgs, "invalid arguments: %v", err)
	}
	return nil
}

func (o *Ops) diskSmart(ctx context.Context, args DiskSmartArgs) (storage.SmartReport, *Error) {
	if !validate.DiskID(args.ID) {
		return storage.SmartReport{}, errorf(CodeInvalidArgs, "invalid disk id")
	}
	if strings.HasPrefix(args.ID, storage.LabIDPrefix) {
		return storage.SmartReport{Reason: storage.SmartNotSupported, Attributes: []storage.SmartAttribute{}}, nil
	}
	// The id was validated (no separator, no ".."): the join cannot escape ByIDDir.
	devPath := path.Join(o.ByIDDir, args.ID)
	if !o.deviceExists(devPath) {
		return storage.SmartReport{}, errorf(CodeDiskNotFound, "no such disk")
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	report, retry, err := o.smartctl(ctx, devPath, "")
	if err != nil {
		return report, err
	}
	if retry {
		// USB bridges often need SCSI/ATA translation to be forced.
		first := report.Reason
		report, _, err = o.smartctl(ctx, devPath, "sat")
		if err != nil {
			return report, err
		}
		if !report.Available && first == storage.SmartUSBBridgeUnsupported {
			report.Reason = first
		}
	}
	return report, nil
}

func (o *Ops) smartctl(ctx context.Context, path, deviceType string) (storage.SmartReport, bool, *Error) {
	res, err := o.Run.Run(ctx, "smartctl", storage.SmartArgs(path, deviceType)...)
	if errors.Is(err, sysexec.ErrNotFound) {
		return storage.SmartReport{Reason: storage.SmartNotInstalled, Attributes: []storage.SmartAttribute{}}, false, nil
	}
	if err != nil {
		return storage.SmartReport{}, false, errorf(CodeFailed, "smartctl: %v", err)
	}
	report, retry := storage.ParseSmartctl(res.Stdout)
	return report, retry, nil
}

func (o *Ops) setHostname(ctx context.Context, args SetHostnameArgs) (SetHostnameArgs, *Error) {
	if !validate.Hostname(args.Hostname) {
		return args, errorf(CodeHostname, "invalid hostname")
	}
	if err := o.check(ctx, "hostnamectl", "set-hostname", args.Hostname); err != nil {
		return args, err
	}
	// Debian resolves its own name through the 127.0.1.1 line of /etc/hosts.
	hosts, err := fs.ReadFile(o.FS, "etc/hosts")
	if err != nil {
		hosts = []byte("127.0.0.1\tlocalhost\n")
	}
	if err := o.WriteFile("/etc/hosts", []byte(updateHosts(string(hosts), args.Hostname)), 0o644); err != nil {
		return args, errorf(CodeFailed, "update /etc/hosts: %v", err)
	}
	return args, nil
}

func (o *Ops) setTimezone(ctx context.Context, args SetTimezoneArgs) (SetTimezoneArgs, *Error) {
	if !validate.Timezone(args.Timezone) {
		return args, errorf(CodeTimezone, "invalid timezone")
	}
	info, err := fs.Stat(o.FS, "usr/share/zoneinfo/"+args.Timezone)
	if err != nil || !info.Mode().IsRegular() {
		return args, errorf(CodeTimezone, "unknown timezone")
	}
	return args, o.check(ctx, "timedatectl", "set-timezone", args.Timezone)
}

// check runs a command that must exit 0.
func (o *Ops) check(ctx context.Context, name string, args ...string) *Error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := o.Run.Run(ctx, name, args...)
	if err != nil {
		return errorf(CodeFailed, "%s: %v", name, err)
	}
	if res.ExitCode != 0 {
		return errorf(CodeFailed, "%s exited with %d: %s", name, res.ExitCode, firstLine(res.Stderr))
	}
	return nil
}

// updateHosts sets the 127.0.1.1 line to hostname, adding it if missing.
func updateHosts(content, hostname string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	entry := "127.0.1.1\t" + hostname
	replaced := false
	for i, line := range lines {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "127.0.1.1" {
			lines[i] = entry
			replaced = true
		}
	}
	if !replaced {
		at := len(lines)
		for i, line := range lines {
			if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "127.0.0.1" {
				at = i + 1
				break
			}
		}
		lines = append(lines[:at], append([]string{entry}, lines[at:]...)...)
	}
	return strings.Join(lines, "\n") + "\n"
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// deviceExists falls back to the real check when DeviceExists is not set.
func (o *Ops) deviceExists(path string) bool {
	if o.DeviceExists != nil {
		return o.DeviceExists(path)
	}
	return isBlockDevice(path)
}

func isBlockDevice(path string) bool {
	info, err := os.Stat(path) // follows the by-id symlink
	return err == nil && info.Mode()&fs.ModeDevice != 0 && info.Mode()&fs.ModeCharDevice == 0
}

// atomicWrite writes data to a temp file in the same directory, syncs it and
// renames it over path, so readers never see a half-written file.
func atomicWrite(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".cwnsb-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

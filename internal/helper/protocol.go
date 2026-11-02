// SPDX-License-Identifier: AGPL-3.0-or-later

// Package helper implements the privilege boundary between nasbusterd
// (unprivileged) and nbhelper (root), see ADR-0002.
//
// Protocol: one request per Unix-socket connection, one JSON line each way.
// The helper only runs named operations from a fixed list, re-validates every
// argument, and never runs a shell.
package helper

import (
	"encoding/json"
	"fmt"

	"github.com/crosswax/nasbuster/internal/apps"
	"github.com/crosswax/nasbuster/internal/shares"
)

// Operation names. Adding one means: a typed args struct, strict validation,
// a handler in ops.go, and a test.
const (
	OpPing        = "ping"
	OpDiskSmart   = "disk.smart"
	OpSetHostname = "system.set_hostname"
	OpSetTimezone = "system.set_timezone"
	OpPoolCreate  = "pool.create"
	OpPoolStatus  = "pool.status"
	OpPoolScrub   = "pool.scrub_start"

	OpPoolMountDegraded = "pool.mount_degraded"
	OpPoolReplace       = "pool.replace"
	OpPoolRestore       = "pool.restore_redundancy"
	OpPoolClearErrors   = "pool.clear_errors"
	OpPoolResume        = "pool.resume"
	OpPoolUnitsRefresh  = "pool.units_refresh"
	OpEngineRefresh     = "engine.config_refresh"
	OpJournalPersist    = "journal.persist"
	OpInitramfsRefresh  = "initramfs.refresh"
	OpPoolReassemble    = "pool.reassemble"
	OpPoolRepairParity  = "pool.repair_parity"
	OpPoolScrubCancel   = "pool.scrub_cancel"
	OpPoolScrubResume   = "pool.scrub_resume"
	OpDiskWipe          = "disk.wipe"
	OpDiskLocate        = "disk.locate"
	OpPoolDestroy       = "pool.destroy"
	OpPoolsScan         = "pool.scan"
	OpPoolImport        = "pool.import"
	OpConfigBackup      = "config.backup"
	OpConfigBackups     = "config.list"
	OpConfigRestore     = "config.restore"
	OpImportSources     = "import.sources"
	OpImportMount       = "import.mount"
	OpImportCopy        = "import.copy"
	OpImportProgress    = "import.progress"
	OpImportUnmount     = "import.unmount"

	OpUserCreate      = "user.create"
	OpUserSetPassword = "user.set_password"
	OpUserDelete      = "user.delete"
	OpShareCreate     = "share.create"
	OpShareDelete     = "share.delete"
	OpRecyclePurge    = "share.recycle_purge"
	OpSMBApply        = "smb.apply"
	OpSMBSessions     = "smb.sessions"

	// OpMachineAccountVerify proves the first admin owns the machine (ADR-0018).
	OpMachineAccountVerify = "auth.verify_machine_account"

	OpSnapshotCreate = "snapshot.create"
	OpSnapshotList   = "snapshot.list"
	OpSnapshotDelete = "snapshot.delete"

	OpEngineStatus  = "apps.engine_status"
	OpEngineInstall = "apps.engine_install"
	OpAppsStatus    = "apps.status"
	OpAppJob        = "apps.job"
	OpAppDeploy     = "app.deploy"
	OpAppAction     = "app.action"
	OpAppRemove     = "app.remove"
	OpAppTrashPurge = "app.trash_purge" // ADR-0035
	OpAppOrphan     = "app.orphan"      // ADR-0036
	OpAppLogs       = "app.logs"
	OpAppUpdate     = "app.update"
	OpEngineRemove  = "apps.engine_remove"
)

// Error codes returned by the helper. Validation codes match the API ones.
const (
	CodeInvalidRequest = "helper.invalid_request"
	CodeUnknownOp      = "helper.unknown_op"
	CodeInvalidArgs    = "helper.invalid_args"
	CodeForbidden      = "helper.forbidden"
	CodeFailed         = "helper.failed"
	CodeDiskNotFound   = "storage.disk_not_found"
	CodeHostname       = "validation.hostname"
	CodeTimezone       = "validation.timezone"

	CodePoolName         = "validation.pool_name"
	CodeLayout           = "validation.layout"
	CodeConfirm          = "validation.confirm"
	CodeDiskCount        = "validation.disk_count"
	CodeDiskNotAvailable = "storage.disk_not_available"
	// CodeDiskResidual: a disk that looked blank still holds an old file system
	// where the new partition starts (wiped elsewhere by removing only its
	// partition table). Nothing is overwritten: the user erases it explicitly.
	CodeDiskResidual    = "storage.disk_residual"
	CodeDiskTooSmall    = "storage.disk_too_small"
	CodePoolExists      = "storage.pool_exists"
	CodePoolNotMounted  = "storage.pool_not_mounted"
	CodePoolNotFound    = "storage.pool_not_found"
	CodePoolMounted     = "storage.pool_mounted"
	CodePoolBusy        = "storage.pool_busy"
	CodeNothingMissing  = "storage.no_missing_disk"
	CodeDiskMissing     = "storage.disk_missing"
	CodeMemberNotFound  = "storage.member_not_found"
	CodeMemberHealthy   = "storage.member_healthy"
	CodeScrubRequired   = "storage.scrub_required"
	CodeDiskProtected   = "storage.disk_protected"
	CodeDiskMounted     = "storage.disk_mounted"
	CodeDiskBusy        = "storage.disk_busy"
	CodeDiskInPool      = "storage.disk_in_pool"
	CodePoolNotEmpty    = "storage.pool_not_empty"
	CodePoolInUse       = "storage.pool_in_use"
	CodePoolIncomplete  = "storage.pool_incomplete"
	CodeNothingToResume = "storage.nothing_to_resume"
	CodePoolBroken      = "storage.pool_broken" // more disks lost than the layout tolerates
	// CodeCannotReassemble: the conditions of a forced assembly are not met
	// (params.reason: not_broken, missing_disks, roles, events_gap).
	CodeCannotReassemble = "storage.cannot_reassemble"
	CodeScrubNotRunning  = "storage.scrub_not_running"
	CodeScrubNotStopped  = "storage.scrub_not_stopped"
	CodeBackupInvalid    = "backup.invalid"
	CodeImportNotMounted = "import.not_mounted"

	CodeAppName         = "validation.app_name"
	CodeComposeInvalid  = "apps.compose_invalid"
	CodeEngineMissing   = "apps.engine_missing"
	CodeEngineInstalled = "apps.engine_installed"
	CodeJobRunning      = "apps.job_running"
	CodeAppNotFound     = "apps.not_found"
	CodeEngineInUse     = "apps.engine_in_use"

	// CodeMachineAccountDenied covers every refusal of OpMachineAccountVerify
	// (unknown account, not an administrator, wrong password, locked account):
	// one code and one duration, so the answer reveals nothing else.
	CodeMachineAccountDenied = "auth.machine_account_denied"

	CodeUserName      = "validation.user_name"
	CodeUserPassword  = "validation.user_password"
	CodeShareName     = "validation.share_name"
	CodeUserExists    = "users.exists"
	CodeUserNameTaken = "users.name_taken"
	CodeUserNotFound  = "users.not_found"
	CodeShareExists   = "shares.exists"
	CodeShareMissing  = "shares.path_missing"
	CodeSnapshotName  = "validation.snapshot_name"
	CodeSnapshotExist = "snapshots.exists"
	CodeSnapshotNone  = "snapshots.not_found"
)

// MaxMessage bounds a request or response line.
const MaxMessage = 1 << 20

// Request is sent by nasbusterd.
type Request struct {
	Op    string          `json:"op"`
	Args  json.RawMessage `json:"args,omitempty"`
	Actor string          `json:"actor,omitempty"` // for the helper's audit log only
}

// Response is sent back by nbhelper.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a typed failure crossing the boundary.
type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"` // structured details for the UI
}

// with attaches structured details to an error.
func (e *Error) with(params map[string]any) *Error {
	e.Params = params
	return e
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// PingResult answers OpPing.
type PingResult struct {
	Pong    bool   `json:"pong"`
	Version string `json:"version"`
}

// DiskSmartArgs addresses a disk by its /dev/disk/by-id basename.
type DiskSmartArgs struct {
	ID string `json:"id"`
}

// SetHostnameArgs for OpSetHostname.
type SetHostnameArgs struct {
	Hostname string `json:"hostname"`
}

// SetTimezoneArgs for OpSetTimezone.
type SetTimezoneArgs struct {
	Timezone string `json:"timezone"`
}

// PoolCreateArgs for OpPoolCreate. Confirm must repeat Name (typed by the user).
type PoolCreateArgs struct {
	Name    string   `json:"name"`
	Layout  string   `json:"layout"`
	Disks   []string `json:"disks"` // /dev/disk/by-id basenames (or lab-loopN)
	Confirm string   `json:"confirm"`
}

// PoolCreateResult answers OpPoolCreate.
type PoolCreateResult struct {
	Name       string   `json:"name"`
	Layout     string   `json:"layout"`
	UUID       string   `json:"uuid"`
	MDDevice   string   `json:"mdDevice"`
	Members    []string `json:"members"`
	MountPoint string   `json:"mountPoint"`
}

// PoolArgs names a pool (OpPoolStatus, OpPoolScrub).
type PoolArgs struct {
	Name string `json:"name"`
}

// DiskWipeArgs erases a disk; Confirm must repeat the id.
type DiskWipeArgs struct {
	ID      string `json:"id"`
	Confirm string `json:"confirm"`
}

// PoolConfirmArgs names a pool for an operation that needs a typed confirmation.
type PoolConfirmArgs struct {
	Name    string `json:"name"`
	Confirm string `json:"confirm"` // must equal Name
}

// PoolResumeArgs takes back a disk that was unplugged and came back (ADR-0027).
type PoolResumeArgs struct {
	Name    string   `json:"name"`
	Confirm string   `json:"confirm"` // must equal Name
	Shares  []string `json:"shares"`  // the pool's SMB shares, closed before the remount
}

// EngineRefreshResult answers OpEngineRefresh.
type EngineRefreshResult struct {
	Updated  bool `json:"updated"`  // daemon.json rewritten (live-restore off)
	Reloaded bool `json:"reloaded"` // Docker was running and reloaded it
}

// PoolReassembleArgs restarts a parity pool whose array lost more disks than it
// tolerates, once they are all back (ADR-0032).
type PoolReassembleArgs struct {
	Name    string   `json:"name"`
	Confirm string   `json:"confirm"` // must equal Name
	Shares  []string `json:"shares"`  // closed before an unmount
}

// PoolReassembleResult answers OpPoolReassemble. The parity repair it owes is
// started by the scheduler (OpPoolRepairParity) once every member is in sync.
type PoolReassembleResult struct {
	Members []string `json:"members"` // assembled by force
	ReAdded []string `json:"reAdded"` // left out by mdadm, taken back (bitmap, else a rebuild)
	LeftOut []string `json:"leftOut"` // left out, and still out: the pool runs degraded
	Mounted bool     `json:"mounted"`
}

// JournalPersistArgs asks for the system journal to be kept across reboots, or
// not (ADR-0031). Status only reports, and changes nothing.
type JournalPersistArgs struct {
	Enabled bool `json:"enabled"`
	Status  bool `json:"status"`
}

// JournalPersistResult answers OpJournalPersist.
type JournalPersistResult struct {
	Persistent bool   `json:"persistent"` // NasBuster's drop-in is in place
	Changed    bool   `json:"changed"`    // written or removed now
	ManagedBy  string `json:"managedBy"`  // another file sets Storage=: left alone
}

// InitramfsResult answers OpInitramfsRefresh (ADR-0033).
type InitramfsResult struct {
	// State: "unsupported" (no update-initramfs), "current" (the initramfs has
	// this mdadm.conf), "running", "started" (rebuild started now), or "failed"
	// (the last rebuild failed: retried at the next call).
	State string `json:"state"`
	// LastError is the failed rebuild's error line, reported once.
	LastError string `json:"lastError,omitempty"`
}

// PoolUnitsResult answers OpPoolUnitsRefresh: the pools whose unit was rewritten.
type PoolUnitsResult struct {
	Updated []string `json:"updated"`
}

// PoolResumeResult answers OpPoolResume.
type PoolResumeResult struct {
	Remounted     bool `json:"remounted"`
	ResyncStarted bool `json:"resyncStarted"` // conversion of single chunks, then a scrub
}

// PoolReplaceArgs replaces member Old (missing, faulty or failing) with the blank disk New.
type PoolReplaceArgs struct {
	Name    string `json:"name"`
	Old     string `json:"old"` // by-id basename, as recorded at creation
	New     string `json:"new"`
	Confirm string `json:"confirm"`
}

// PoolReplaceResult says how the replacement was started (it runs in the background).
type PoolReplaceResult struct {
	Old    string `json:"old"`
	New    string `json:"new"`
	DevID  int    `json:"devId,omitempty"` // Btrfs devid being rebuilt
	Method string `json:"method"`          // btrfs-replace, md-add
}

// EngineStatus answers OpEngineStatus.
type EngineStatus struct {
	Installed bool     `json:"installed"`
	Version   string   `json:"version"`
	Active    bool     `json:"active"`
	Pool      string   `json:"pool"` // where Docker keeps its data ("" if not set by CWNSB)
	Job       apps.Job `json:"job"`  // installation job
	// DataPools: while Docker is not installed, the pools that still hold
	// the data of an earlier installation (P9). Installed there, Docker
	// takes it back, containers included.
	DataPools []EngineData `json:"dataPools"`
}

// EngineData is a Docker data folder on a pool, and how many containers it holds.
type EngineData struct {
	Pool       string `json:"pool"`
	Containers int    `json:"containers"`
}

// EngineInstallArgs installs Docker with its data on Pool.
type EngineInstallArgs struct {
	Pool string `json:"pool"`
}

// AppDeployArgs writes and starts an application.
type AppDeployArgs struct {
	Pool    string `json:"pool"`
	Name    string `json:"name"`
	Compose string `json:"compose"`
}

// AppActionArgs starts, stops or restarts an application.
type AppActionArgs struct {
	Pool   string `json:"pool"`
	Name   string `json:"name"`
	Action string `json:"action"`
}

// AppRemoveArgs removes an application (and its data if DeleteData).
type AppRemoveArgs struct {
	Pool       string `json:"pool"`
	Name       string `json:"name"`
	DeleteData bool   `json:"deleteData"`
}

// TrashPurgeArgs names the pool whose removed applications' data, kept
// for apps.TrashDays, is purged (ADR-0035).
type TrashPurgeArgs struct {
	Pool string `json:"pool"`
}

// OrphanArgs stops or removes ("stop", "remove") the containers of a CWNSB
// project whose application is not recorded (ADR-0036).
type OrphanArgs struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

// AppLogsArgs reads the end of an application's logs.
type AppLogsArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
	Tail int    `json:"tail"`
}

// EngineRemoveArgs uninstalls Docker; DeleteData also deletes its data-root
// (images, containers' layers, named volumes) on the pool.
type EngineRemoveArgs struct {
	DeleteData bool   `json:"deleteData"`
	Pool       string `json:"pool,omitempty"` // Docker not installed any more: the pool whose kept data to delete
}

// JobArgs names a job: "engine" or "app-<name>".
type JobArgs struct {
	Job string `json:"job"`
}

// ProjectView is a Compose project with its containers. App is set for
// CWNSB applications (project "cwnsb-<app>").
type ProjectView struct {
	apps.ProjectStatus
	App        string           `json:"app"`
	Containers []apps.Container `json:"containers"`
}

// AppsStatus answers OpAppsStatus.
type AppsStatus struct {
	Engine   EngineStatus  `json:"engine"`
	Projects []ProjectView `json:"projects"`
}

// BackupArgs describes, for the manifest, what the daemon's database holds:
// the installation that makes the backup (its rotation only touches its own,
// P6), the admin and the shares a restore brings back.
type BackupArgs struct {
	Installation string   `json:"installation"`
	Admin        string   `json:"admin"`
	Shares       []string `json:"shares"`
}

// BackupResult answers OpConfigBackup.
type BackupResult struct {
	Name  string   `json:"name"`
	Pools []string `json:"pools"` // where it was stored
}

// ImportSource is a foreign filesystem that can be copied from (ADR-0015).
type ImportSource struct {
	UUID       string   `json:"uuid"`
	FSType     string   `json:"fstype"`
	Label      string   `json:"label"`
	Device     string   `json:"device"` // kernel name (sdb1, md127)
	SizeBytes  uint64   `json:"sizeBytes"`
	Disks      []string `json:"disks"` // by-id of the disks holding it
	Mounted    bool     `json:"mounted"`
	MountPoint string   `json:"mountPoint"`
}

// ImportSources answers OpImportSources.
type ImportSources struct {
	Sources []ImportSource `json:"sources"`
}

// ImportMountArgs names a source by filesystem UUID.
type ImportMountArgs struct {
	UUID string `json:"uuid"`
}

// ImportMounted answers OpImportMount.
type ImportMounted struct {
	MountPoint string   `json:"mountPoint"`
	Entries    []string `json:"entries"` // top-level names (200 at most)
}

// ImportCopyArgs copies a source (or a folder of it) into a share.
type ImportCopyArgs struct {
	UUID   string `json:"uuid"`
	Pool   string `json:"pool"`
	Share  string `json:"share"`
	Subdir string `json:"subdir"`
}

// ImportShareArgs names the share of a copy.
type ImportShareArgs struct {
	Share string `json:"share"`
}

// ImportProgress answers OpImportProgress.
type ImportProgress struct {
	Job         apps.Job `json:"job"`
	Source      string   `json:"source"`
	Dest        string   `json:"dest"`
	TotalBytes  int64    `json:"totalBytes"` // 0: unknown
	CopiedBytes int64    `json:"copiedBytes"`
	Files       int      `json:"files"`
}

// RecyclePurgeArgs empties what a share's recycle bin has kept longer than Days.
type RecyclePurgeArgs struct {
	Pool  string `json:"pool"`
	Share string `json:"share"`
	Days  int    `json:"days"`
}

// RestoreArgs names a backup to restore.
type RestoreArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
}

// RestoreConfigResult answers OpConfigRestore.
type RestoreConfigResult struct {
	Users     []string `json:"users"`     // NAS accounts restored (or already right)
	Conflicts []string `json:"conflicts"` // skipped, with the reason
	Restart   bool     `json:"restart"`   // the daemon must restart to load the database
}

// BackupInfo is one backup found on a pool.
type BackupInfo struct {
	Pool      string `json:"pool"`
	Name      string `json:"name"`
	Time      int64  `json:"time"`
	Hostname  string `json:"hostname"`
	Users     int    `json:"users"`
	SizeBytes int64  `json:"sizeBytes"`
	// From version 2 manifests (P6); Shares is -1 when unknown.
	Installation string `json:"installation"`
	Admin        string `json:"admin"`
	Shares       int    `json:"shares"`
	// ThisInstallation is filled in by the daemon, which knows its own id.
	ThisInstallation bool `json:"thisInstallation"`
}

// BackupList answers OpConfigBackups.
type BackupList struct {
	Backups []BackupInfo `json:"backups"`
}

// ImportCandidate is a CWNSB pool found on disks this system does not know.
type ImportCandidate struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`   // btrfs, md
	Layout   string   `json:"layout"` // best guess before import
	Present  int      `json:"present"`
	Expected int      `json:"expected"`
	Complete bool     `json:"complete"`
	Disks    []string `json:"disks"`
}

// ImportScan answers OpPoolsScan.
type ImportScan struct {
	Pools []ImportCandidate `json:"pools"`
}

// PoolDestroyResult answers OpPoolDestroy.
type PoolDestroyResult struct {
	Destroyed bool     `json:"destroyed"` // members erased
	Forgotten bool     `json:"forgotten"` // disks gone: configuration removed only
	Disks     []string `json:"disks"`     // erased disks (by-id)
}

// RestoreResult answers OpPoolRestore.
type RestoreResult struct {
	Resized        int    `json:"resized"`
	BalanceStarted bool   `json:"balanceStarted"`
	Data           string `json:"data,omitempty"`
	Metadata       string `json:"metadata,omitempty"`
}

// UserArgs for OpUserCreate, OpUserSetPassword and OpUserDelete (no password).
type UserArgs struct {
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
}

// MachineAccountArgs for OpMachineAccountVerify. The password is never logged
// nor stored: it only goes to the system's checker, on standard input.
type MachineAccountArgs struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// MachineAccountResult answers a successful OpMachineAccountVerify.
type MachineAccountResult struct {
	Verified bool `json:"verified"`
}

// ShareCreateArgs for OpShareCreate.
type ShareCreateArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
}

// SMBApplyArgs carries the COMPLETE desired list of shares. The helper renders
// smb.conf itself from it: no configuration text ever crosses the boundary.
type SMBApplyArgs struct {
	Shares []shares.Share `json:"shares"`
}

// SMBSessionsResult answers OpSMBSessions.
type SMBSessionsResult struct {
	Sessions    []shares.Session    `json:"sessions"`
	Connections []shares.Connection `json:"connections"`
}

// SnapshotArgs addresses a share (and a snapshot for OpSnapshotDelete).
type SnapshotArgs struct {
	Pool  string `json:"pool"`
	Share string `json:"share"`
	Name  string `json:"name,omitempty"`
}

// SnapshotListResult answers OpSnapshotList (names, newest first).
type SnapshotListResult struct {
	Snapshots []string `json:"snapshots"`
}

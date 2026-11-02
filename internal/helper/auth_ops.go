// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/validate"
)

// machineAdminGroup is the group of Debian's administrators (Raspberry Pi OS
// puts the account created in Imager in it).
const machineAdminGroup = "sudo"

// machineCheckFloor is the shortest time a refusal takes. unix_chkpwd answers
// an unknown account in about 2 ms and a wrong password in about 23 ms
// (measured in the lab): padding every refusal hides the difference and slows
// guessing down, on top of the API's rate limiter.
const machineCheckFloor = time.Second

// verifyMachineAccount proves that the person creating the first admin owns
// the machine (ADR-0018): the account must be an administrator of the machine
// (root, or a member of the sudo group) and the password must be its own,
// checked by the system's own checker (unix_chkpwd, from PAM: every hash
// format, locked accounts refused). Every refusal has the same code and lasts
// at least machineCheckFloor. The password goes to unix_chkpwd on standard
// input only, terminated by a NUL byte as it expects.
func (o *Ops) verifyMachineAccount(ctx context.Context, a MachineAccountArgs) (MachineAccountResult, *Error) {
	start := o.now()
	deny := func() (MachineAccountResult, *Error) {
		if wait := machineCheckFloor - o.now().Sub(start); wait > 0 {
			o.sleep(wait)
		}
		return MachineAccountResult{}, errorf(CodeMachineAccountDenied, "machine account not accepted")
	}

	if !validate.MachineUser(a.User) || !validate.MachinePassword(a.Password) {
		return deny()
	}
	if !o.isMachineAdmin(ctx, a.User) {
		return deny()
	}
	input := append([]byte(a.Password), 0)
	res, err := o.Run.RunInput(ctx, input, "unix_chkpwd", a.User, "nonull")
	if err != nil || res.ExitCode != 0 {
		return deny()
	}
	return MachineAccountResult{Verified: true}, nil
}

// isMachineAdmin reports whether user may administer the machine: root, or a
// member of the sudo group. Any doubt (unknown account, id failing) is a no.
func (o *Ops) isMachineAdmin(ctx context.Context, user string) bool {
	if user == "root" {
		return true
	}
	res, err := o.Run.Run(ctx, "id", "-nG", user)
	if err != nil || res.ExitCode != 0 {
		return false
	}
	return slices.Contains(strings.Fields(string(res.Stdout)), machineAdminGroup)
}

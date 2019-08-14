package mgr

import (
	"fmt"
	"time"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/pkg/errtypes"
	"github.com/alibaba/pouch/pkg/utils"

	"github.com/pkg/errors"
)

// IsRunning returns container is running or not.
func (c *Container) IsRunning() bool {
	return c.State.Status == types.StatusRunning && c.State.Running
}

// IsPaused returns container is paused or not.
func (c *Container) IsPaused() bool {
	return c.State.Status == types.StatusPaused && c.State.Paused
}

// IsRunningOrPaused returns true of container is running or paused.
func (c *Container) IsRunningOrPaused() bool {
	return c.IsRunning() || c.IsPaused()
}

// IsExited returns container is exited or not.
func (c *Container) IsExited() bool {
	return c.State.Status == types.StatusExited && c.State.Exited
}

// IsRestarting returns container is restarting or not.
func (c *Container) IsRestarting() bool {
	return c.State.Status == types.StatusRestarting && c.State.Restarting
}

// IsCreated returns container is created or not.
func (c *Container) IsCreated() bool {
	return c.State.Status == types.StatusCreated
}

// IsDead returns container is dead or not.
// NOTE: ContainerMgmt.Remove action will set Dead to container's meta config
// before removing the meta config json file.
func (c *Container) IsDead() bool {
	return c.State.Status == types.StatusDead && c.State.Dead
}

// SetStatus is used to set status.
func (c *Container) SetStatus(s types.Status) {
	c.setStatusFlags(s)
}

// GetStatus returns the container's status.
func (c *Container) GetStatus() types.Status {
	return c.State.Status
}

// SetStatusRunning sets a container to be status running.
// When a container's status turns to StatusStopped, the following fields need updated:
// Status -> StatusRunning
// StartAt -> time.Now()
// Pid -> input param
// ExitCode -> 0
func (c *Container) SetStatusRunning(pid int64) {
	c.State.StartedAt = time.Now().UTC().Format(utils.TimeLayout)
	c.State.Pid = pid
	c.State.ExitCode = 0
	c.setStatusFlags(types.StatusRunning)
}

// SetStatusRestarting set a container to be restarting status.
func (c *Container) SetStatusRestarting() {
	c.setStatusFlags(types.StatusRestarting)
}

// SetStatusExited sets a container to be status exited.
func (c *Container) SetStatusExited(exitCode int64, errMsg string) {
	c.State.FinishedAt = time.Now().UTC().Format(utils.TimeLayout)
	c.State.Pid = 0
	c.State.ExitCode = exitCode
	c.State.Error = errMsg
	c.setStatusFlags(types.StatusExited)
}

// SetStatusPaused sets a container to be status paused.
func (c *Container) SetStatusPaused() {
	c.setStatusFlags(types.StatusPaused)
}

// SetStatusUnpaused sets a container to be status running.
// Unpaused is treated running.
func (c *Container) SetStatusUnpaused() {
	c.setStatusFlags(types.StatusRunning)
}

// SetStatusDead sets a container to be status dead.
func (c *Container) SetStatusDead() {
	c.setStatusFlags(types.StatusDead)
}

// SetStatusOOM sets a container to be status exit because of OOM.
func (c *Container) SetStatusOOM() {
	c.State.OOMKilled = true
	c.State.Error = "OOMKilled"
}

// Notes(ziren): i still feel uncomfortable for a function hasing no return
// setStatusFlags set the specified status flag to true, and unset others
func (c *Container) setStatusFlags(status types.Status) {
	c.State.Status = status

	statusFlags := map[types.Status]bool{
		types.StatusDead:       false,
		types.StatusRunning:    false,
		types.StatusPaused:     false,
		types.StatusRestarting: false,
		types.StatusExited:     false,
	}

	if _, exists := statusFlags[status]; exists {
		statusFlags[status] = true
	}

	for k, v := range statusFlags {
		switch k {
		case types.StatusDead:
			c.State.Dead = v
		case types.StatusPaused:
			c.State.Paused = v
		case types.StatusRunning:
			c.State.Running = v
		case types.StatusRestarting:
			c.State.Restarting = v
		case types.StatusExited:
			c.State.Exited = v
		}
	}
}

// ExitCode returns container's ExitCode.
func (c *Container) ExitCode() int64 {
	return c.State.ExitCode
}

func (c *Container) validateStartContainerStatus() error {
	// check if container's status is paused
	if c.IsPaused() {
		return fmt.Errorf("cannot start a paused container %s, try unpause instead", c.ID)
	}

	// check if container's status is running
	if c.IsRunning() {
		return errors.Wrapf(errtypes.ErrNotModified, "container %s already started", c.ID)
	}

	if c.IsRestarting() {
		return errors.Errorf("can not start a restarting container %s", c.ID)
	}

	if c.IsDead() {
		return fmt.Errorf("cannot start a dead container %s", c.ID)
	}

	return nil
}

func (c *Container) validateStopContainerStatue() error {
	return nil
}

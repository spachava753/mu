package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spachava753/acp-sdk/acp"
)

// Rename gives a thread the user's title, which agent updates and discovery no
// longer replace. The new title is saved locally first. If the thread's agent
// is running and stores titles, it is told too; a thread whose agent is not
// running, or does not store titles, keeps the title in Mu only. A title that
// is already the thread's changes nothing.
func (a *App) Rename(id, title string) error {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return errors.New("enter a title")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errAppClosing
	}
	t := a.threadLocked(id)
	if t == nil {
		return errors.New("thread not found")
	}
	if title == t.Title {
		return nil
	}
	oldTitle, oldRenamed := t.Title, t.Renamed
	t.Title, t.Renamed = title, true
	if err := a.saveLocked(); err != nil {
		t.Title, t.Renamed = oldTitle, oldRenamed
		return err
	}
	a.syncTitleLocked(id, a.runtimeLocked(id), false)
	return nil
}

// syncTitleLocked tells the thread's agent its title, if it is running and stores
// titles. A quiet title is Mu's own idea, from the first message, so an agent
// refusing it is not worth the user's attention.
func (a *App) syncTitleLocked(id string, r *runtime, quiet bool) {
	t := a.threadLocked(id)
	if t == nil {
		return
	}
	r.titleGeneration++
	r.titleError, r.titleQuiet = "", quiet
	if r.titleSyncing {
		return // The running worker sends the newest title next.
	}
	c := a.connections[[2]string{t.AgentID, t.Command}]
	if t.SessionID == "" || !c.alive() || !c.process.SessionTitles() {
		return
	}
	c.users++ // Released when the worker ends or the thread detaches.
	r.titleSyncing, r.titleConn = true, c
	a.wg.Add(1)
	go a.storeTitles(c, r, id, t.SessionID)
}

// storeTitles asks the agent to store the thread's title, one request at a
// time in the order the user renamed, so that the agent ends with the latest.
// Renames made during a request coalesce into one more request. Only the
// outcome for the latest title is applied.
func (a *App) storeTitles(c *connection, r *runtime, id, session string) {
	defer a.wg.Done()
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() {
		r.titleSyncing = false
		if r.titleConn != nil {
			r.titleConn = nil
			a.releaseLocked(c)
		}
	}()
	for {
		t := a.threadLocked(id)
		if t == nil || a.closed {
			return
		}
		title, generation := t.Title, r.titleGeneration
		a.mu.Unlock()
		stored, err := a.requestTitle(c, session, title)
		a.mu.Lock()
		if t = a.threadLocked(id); t == nil {
			return
		}
		if r.titleGeneration != generation {
			continue
		}
		if err != nil {
			if r.titleQuiet {
				return
			}
			r.titleError = fmt.Sprintf("The title was kept in Mu, but the agent did not store it: %v", c.process.Error(err))
		} else if stored != "" && stored != title {
			t.Title = stored
			_ = a.saveLocked()
		}
		a.notifyLocked()
		return
	}
}

// requestTitle waits for the agent's answer, with no deadline of its own: a
// write that timed out here could still finish after a newer one and replace
// it. Only the process ending or the app closing ends the wait.
func (a *App) requestTitle(c *connection, session, title string) (string, error) {
	defer a.unblockAtShutdown(c)()
	response, err := c.process.Conn.SessionSetTitle(a.ctx, &acp.SetSessionTitleRequest{SessionID: acp.SessionId(session), Title: title})
	if err != nil {
		return "", err
	}
	return response.Title, nil
}

// DismissTitleError hides a thread's failure to store its title with the agent.
func (a *App) DismissTitleError(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r := a.runtimes[id]; r != nil {
		r.titleError = ""
		a.notifyLocked()
	}
}

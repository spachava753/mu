package desktop

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
)

func (v *View) openRename(t app.ThreadView) {
	v.renameID, v.renameTitle, v.renameError, v.renaming, v.renameFresh = t.ID, t.Title, "", true, true
}

func (v *View) renameDialog(c *ui.Context) {
	modal(c, &v.renaming, func() {
		save := func() {
			if strings.TrimSpace(v.renameTitle) == "" {
				return
			}
			if err := v.app.Rename(v.renameID, v.renameTitle); err != nil {
				v.renameError = "Could not save the title: " + err.Error() // Stay open, keeping what was typed.
				return
			}
			v.renaming = false
		}
		ui.Text(c, "Rename thread").FontSize(14).FontWeight(500)
		input := ui.TextInput(c.Key("rename-title"), &v.renameTitle).Label("Thread title").Placeholder("Thread title").Width(380).MaxWidthPercent(100).AutoFocus().OnSubmit(save)
		if v.renameFresh {
			// Typing replaces the old title, as in other rename dialogs.
			input.SetTextSelection(0, utf8.RuneCountInString(v.renameTitle))
			v.renameFresh = false
		}
		if v.renameError != "" {
			ui.Text(c, v.renameError).FontSize(12).TextColor(c.Theme().Danger).MaxWidth(380)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Margin(6, 0, 0, 0).Children(func() {
			ui.Button(c, "Cancel").OnClick(func() { v.renaming = false })
			ui.PrimaryButton(c, "Rename").Disabled(strings.TrimSpace(v.renameTitle) == "").OnClick(save)
		})
	})
}

package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"zen-cap/pkg/config"
	"zen-cap/pkg/snippet"
)

// ScrollAccelerate injects multiple Up/Down keys into the primitive's InputHandler.
func ScrollAccelerate(primitive tview.Primitive, speed int) func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
	return func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseScrollUp {
			for i := 0; i < speed; i++ {
				primitive.InputHandler()(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), nil)
			}
			return action, nil // Consume event
		}
		if action == tview.MouseScrollDown {
			for i := 0; i < speed; i++ {
				primitive.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), nil)
			}
			return action, nil // Consume event
		}
		return action, event
	}
}

// RunManager launches the tview Snippet Manager TUI
func RunManager(cfg *config.Config, snippetMgr *snippet.Manager) error {
	app := tview.NewApplication()
	app.EnableMouse(true)

	// Theme custom styling
	tview.Styles.PrimitiveBackgroundColor = tcell.NewRGBColor(26, 26, 36)     // Charcoal Navy
	tview.Styles.BorderColor = tcell.NewRGBColor(0, 240, 255)                // Neon Cyan
	tview.Styles.TitleColor = tcell.NewRGBColor(255, 0, 127)                 // Neon Pink
	tview.Styles.GraphicsColor = tcell.NewRGBColor(120, 120, 150)            // Cool Grey
	tview.Styles.PrimaryTextColor = tcell.NewRGBColor(230, 230, 240)         // Off White
	tview.Styles.SecondaryTextColor = tcell.NewRGBColor(150, 150, 180)       // Muted text
	tview.Styles.ContrastBackgroundColor = tcell.NewRGBColor(45, 55, 85)     // Highlights
	tview.Styles.ContrastSecondaryTextColor = tcell.NewRGBColor(0, 240, 255)

	// Sidebar Snippet List
	snippetList := tview.NewList().ShowSecondaryText(true)
	snippetList.SetBorder(true).SetTitle(" Snippets (0) ")

	// Right side details and forms
	detailView := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	detailView.SetBorder(true).SetTitle(" Preview ")

	formPanel := tview.NewForm()
	formPanel.SetBorder(true).SetTitle(" Add / Edit Snippet ")

	// Layout switcher
	rightPages := tview.NewPages()
	rightPages.AddPage("detail", detailView, true, true)
	rightPages.AddPage("form", formPanel, true, false)

	// Main Layout Split
	mainFlex := tview.NewFlex().SetDirection(tview.FlexColumn)
	mainFlex.AddItem(snippetList, 0, 2, true)
	mainFlex.AddItem(rightPages, 0, 3, false)

	// Status line at bottom
	statusView := tview.NewTextView().SetTextAlign(tview.AlignCenter)
	statusView.SetText("Ctrl+N: New | Ctrl+E: Edit | Ctrl+D: Delete | Ctrl+Q: Quit")
	statusView.SetBackgroundColor(tcell.NewRGBColor(35, 35, 48))

	rootLayout := tview.NewFlex().SetDirection(tview.FlexRow)
	rootLayout.AddItem(mainFlex, 0, 1, true)
	rootLayout.AddItem(statusView, 1, 0, false)

	// Global modals page for Delete confirmations
	rootPages := tview.NewPages()
	rootPages.AddPage("main", rootLayout, true, true)

	// Populate snippet list helper
	refreshSnippets := func() {
		snippetList.Clear()
		snippets := snippetMgr.GetAll()
		snippetList.SetTitle(fmt.Sprintf(" Snippets (%d) ", len(snippets)))

		if len(snippets) == 0 {
			snippetList.AddItem("No snippets yet.", "Press Ctrl+N to create one", ' ', nil)
			detailView.SetText("\n  No snippet selected.")
			return
		}

		for i, snip := range snippets {
			// Create a shortcut index character 1-9, then 0, then a-z
			shortcut := rune(0)
			if i < 9 {
				shortcut = rune('1' + i)
			} else if i == 9 {
				shortcut = rune('0')
			}

			displayName := snip.Name
			preview := strings.ReplaceAll(snip.Content, "\n", " ")
			if len(preview) > 40 {
				preview = preview[:37] + "..."
			}

			snippetList.AddItem(displayName, preview, shortcut, nil)
		}
	}

	// Update detail preview helper
	updatePreview := func() {
		idx := snippetList.GetCurrentItem()
		snippets := snippetMgr.GetAll()
		if idx < 0 || idx >= len(snippets) {
			detailView.SetText("\n  No snippet selected.")
			return
		}

		snip := snippets[idx]
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("\n  [#ff007f]Name:[#ffffff] %s\n", snip.Name))
		sb.WriteString("  [#8c8c9e]--------------------------------------------------\n\n")
		sb.WriteString("  " + strings.ReplaceAll(snip.Content, "\n", "\n  "))

		detailView.SetText(sb.String())
	}

	// Setup selection tracking
	snippetList.SetChangedFunc(func(index int, mainText string, secondaryText string, shortcut rune) {
		updatePreview()
	})

	// Add Snippet Form handler
	showAddForm := func() {
		formPanel.Clear(true)
		nameField := tview.NewInputField().SetLabel("Snippet Name").SetFieldWidth(30)
		contentField := tview.NewTextArea().SetLabel("Content").SetPlaceholder("Enter snippet text...")

		formPanel.AddFormItem(nameField)
		formPanel.AddFormItem(contentField)

		formPanel.AddButton("Save", func() {
			name := strings.TrimSpace(nameField.GetText())
			content := contentField.GetText()
			if name == "" {
				return
			}
			_, _ = snippetMgr.Add(name, content)
			refreshSnippets()
			rightPages.SwitchToPage("detail")
			app.SetFocus(snippetList)
			updatePreview()
		})

		formPanel.AddButton("Cancel", func() {
			rightPages.SwitchToPage("detail")
			app.SetFocus(snippetList)
		})

		rightPages.SwitchToPage("form")
		app.SetFocus(formPanel)
	}

	// Edit Snippet Form handler
	showEditForm := func() {
		idx := snippetList.GetCurrentItem()
		snippets := snippetMgr.GetAll()
		if idx < 0 || idx >= len(snippets) {
			return
		}
		snip := snippets[idx]

		formPanel.Clear(true)
		nameField := tview.NewInputField().SetLabel("Snippet Name").SetText(snip.Name).SetFieldWidth(30)
		contentField := tview.NewTextArea().SetLabel("Content").SetText(snip.Content, false)

		formPanel.AddFormItem(nameField)
		formPanel.AddFormItem(contentField)

		formPanel.AddButton("Update", func() {
			name := strings.TrimSpace(nameField.GetText())
			content := contentField.GetText()
			if name == "" {
				return
			}
			_, _ = snippetMgr.Update(snip.ID, name, content)
			refreshSnippets()
			snippetList.SetCurrentItem(idx)
			rightPages.SwitchToPage("detail")
			app.SetFocus(snippetList)
			updatePreview()
		})

		formPanel.AddButton("Cancel", func() {
			rightPages.SwitchToPage("detail")
			app.SetFocus(snippetList)
		})

		rightPages.SwitchToPage("form")
		app.SetFocus(formPanel)
	}

	// Delete confirmation modal
	showDeleteConfirm := func() {
		idx := snippetList.GetCurrentItem()
		snippets := snippetMgr.GetAll()
		if idx < 0 || idx >= len(snippets) {
			return
		}
		snip := snippets[idx]

		modal := tview.NewModal().
			SetText(fmt.Sprintf("Are you sure you want to delete '%s'?", snip.Name)).
			AddButtons([]string{"Yes", "No"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				if buttonLabel == "Yes" {
					_, _ = snippetMgr.Delete(snip.ID)
					refreshSnippets()
					updatePreview()
				}
				rootPages.RemovePage("delete_confirm")
				app.SetFocus(snippetList)
			})

		rootPages.AddPage("delete_confirm", modal, true, true)
		app.SetFocus(modal)
	}

	// Global Key Bindings
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlQ {
			app.Stop()
			return nil
		}
		if event.Key() == tcell.KeyCtrlN {
			showAddForm()
			return nil
		}
		if event.Key() == tcell.KeyCtrlE {
			showEditForm()
			return nil
		}
		if event.Key() == tcell.KeyCtrlD {
			showDeleteConfirm()
			return nil
		}
		return event
	})

	// Snippet list keyboard short cuts
	snippetList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune {
			switch event.Rune() {
			case 'n', 'N':
				showAddForm()
				return nil
			case 'e', 'E':
				showEditForm()
				return nil
			case 'd', 'D':
				showDeleteConfirm()
				return nil
			}
		}
		return event
	})

	// Initial data loading
	refreshSnippets()
	updatePreview()

	// Apply mouse scroll accelerations & Hover-Focus (from tview-scroll skill)
	app.SetMouseCapture(func(event *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
		x, y := event.Position()
		panels := []tview.Primitive{snippetList, detailView, formPanel}

		var hoveredPanel tview.Primitive
		for _, p := range panels {
			px, py, pw, ph := p.GetRect()
			if x >= px && x < px+pw && y >= py && y < py+ph {
				hoveredPanel = p
				break
			}
		}

		if hoveredPanel != nil {
			// Click to focus
			if action == tview.MouseLeftClick {
				app.SetFocus(hoveredPanel)
			}

			// Scroll-to-focus and Scroll acceleration
			if action == tview.MouseScrollUp || action == tview.MouseScrollDown {
				app.SetFocus(hoveredPanel)
				a, e := ScrollAccelerate(hoveredPanel, 3)(action, event)
				return e, a
			}
		}
		return event, action
	})

	return app.SetRoot(rootPages, true).Run()
}

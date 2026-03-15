package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/jesseduffield/gocui"
	"github.com/jesseduffield/lazydocker/pkg/commands"
	"github.com/jesseduffield/lazydocker/pkg/config"
	"github.com/jesseduffield/lazydocker/pkg/gui/panels"
	"github.com/jesseduffield/lazydocker/pkg/gui/presentation"
	"github.com/jesseduffield/lazydocker/pkg/gui/types"
	"github.com/jesseduffield/lazydocker/pkg/tasks"
	"github.com/jesseduffield/lazydocker/pkg/utils"
	"github.com/samber/lo"
)

func (gui *Gui) getVolumesPanel() *panels.SideListPanel[*commands.Volume] {
	return &panels.SideListPanel[*commands.Volume]{
		ContextState: &panels.ContextState[*commands.Volume]{
			GetMainTabs: func() []panels.MainTab[*commands.Volume] {
				return []panels.MainTab[*commands.Volume]{
					{
						Key:    "config",
						Title:  gui.Tr.ConfigTitle,
						Render: gui.renderVolumeConfig,
					},
					{
						Key:    "files",
						Title:  "Files",
						Render: gui.renderVolumeFiles,
					},
				}
			},
			GetItemContextCacheKey: func(volume *commands.Volume) string {
				return "volumes-" + volume.Name
			},
		},
		ListPanel: panels.ListPanel[*commands.Volume]{
			List: panels.NewFilteredList[*commands.Volume](),
			View: gui.Views.Volumes,
		},
		NoItemsMessage: gui.Tr.NoVolumes,
		Gui:            gui.intoInterface(),
		// we're sorting these volumes based on whether they have labels defined,
		// because those are the ones you typically care about.
		// Within that, we also sort them alphabetically
		Sort: func(a *commands.Volume, b *commands.Volume) bool {
			if len(a.Volume.Labels) == 0 && len(b.Volume.Labels) > 0 {
				return false
			}
			if len(a.Volume.Labels) > 0 && len(b.Volume.Labels) == 0 {
				return true
			}
			return a.Name < b.Name
		},
		GetTableCells: presentation.GetVolumeDisplayStrings,
	}
}

func (gui *Gui) renderVolumeConfig(volume *commands.Volume) tasks.TaskFunc {
	return gui.NewSimpleRenderStringTask(func() string { return gui.volumeConfigStr(volume) })
}

func (gui *Gui) volumeConfigStr(volume *commands.Volume) string {
	padding := 15
	output := ""
	output += utils.WithPadding("Name: ", padding) + volume.Name + "\n"
	output += utils.WithPadding("Driver: ", padding) + volume.Volume.Driver + "\n"
	output += utils.WithPadding("Scope: ", padding) + volume.Volume.Scope + "\n"
	output += utils.WithPadding("Mountpoint: ", padding) + volume.Volume.Mountpoint + "\n"
	output += utils.WithPadding("Labels: ", padding) + utils.FormatMap(padding, volume.Volume.Labels) + "\n"
	output += utils.WithPadding("Options: ", padding) + utils.FormatMap(padding, volume.Volume.Options) + "\n"

	output += utils.WithPadding("Status: ", padding)
	if volume.Volume.Status != nil {
		output += "\n"
		for k, v := range volume.Volume.Status {
			output += utils.FormatMapItem(padding, k, v)
		}
	} else {
		output += "n/a"
	}

	if volume.Volume.UsageData != nil {
		output += utils.WithPadding("RefCount: ", padding) + fmt.Sprintf("%d", volume.Volume.UsageData.RefCount) + "\n"
		output += utils.WithPadding("Size: ", padding) + utils.FormatBinaryBytes(int(volume.Volume.UsageData.Size)) + "\n"
	}

	return output
}

func (gui *Gui) renderVolumeFiles(volume *commands.Volume) tasks.TaskFunc {
	return gui.NewSimpleRenderStringTask(func() string { return gui.volumeFilesStr(volume) })
}

func (gui *Gui) volumeFilesStr(volume *commands.Volume) string {
	mountpoint := volume.Volume.Mountpoint
	if mountpoint == "" {
		return "No mountpoint available for this volume.\n"
	}

	info, err := os.Stat(mountpoint)
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Sprintf("Permission denied reading %s\nTry running lazydocker with elevated privileges.\n", mountpoint)
		}
		return fmt.Sprintf("Error accessing mountpoint: %s\n", err.Error())
	}
	if !info.IsDir() {
		return fmt.Sprintf("Mountpoint %s is not a directory.\n", mountpoint)
	}

	var sb strings.Builder
	sb.WriteString(utils.ColoredString(mountpoint, color.FgCyan) + "\n")

	if err := renderDirTree(mountpoint, "", &sb); err != nil {
		if os.IsPermission(err) {
			sb.WriteString(utils.ColoredString("[permission denied]", color.FgRed) + "\n")
		} else {
			sb.WriteString(utils.ColoredString(fmt.Sprintf("[error: %s]", err.Error()), color.FgRed) + "\n")
		}
	}

	return sb.String()
}

// renderDirTree recursively renders directory entries using box-drawing characters.
func renderDirTree(dirPath string, prefix string, sb *strings.Builder) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		sb.WriteString(prefix + utils.ColoredString("(empty)", color.FgYellow) + "\n")
		return nil
	}

	for i, entry := range entries {
		isLast := i == len(entries)-1

		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		name := entry.Name()
		if entry.IsDir() {
			sb.WriteString(prefix + connector + utils.ColoredString(name+"/", color.FgBlue) + "\n")
			if err := renderDirTree(filepath.Join(dirPath, name), childPrefix, sb); err != nil {
				sb.WriteString(childPrefix + utils.ColoredString("[permission denied]", color.FgRed) + "\n")
			}
		} else {
			info, infoErr := entry.Info()
			if infoErr == nil {
				size := utils.ColoredString("("+utils.FormatBinaryBytes(int(info.Size()))+")", color.FgYellow)
				sb.WriteString(prefix + connector + name + " " + size + "\n")
			} else {
				sb.WriteString(prefix + connector + name + "\n")
			}
		}
	}

	return nil
}

func (gui *Gui) reloadVolumes() error {
	if err := gui.refreshStateVolumes(); err != nil {
		return err
	}

	return gui.Panels.Volumes.RerenderList()
}

func (gui *Gui) refreshStateVolumes() error {
	volumes, err := gui.DockerCommand.RefreshVolumes()
	if err != nil {
		return err
	}

	gui.Panels.Volumes.SetItems(volumes)

	return nil
}

func (gui *Gui) handleVolumesRemoveMenu(g *gocui.Gui, v *gocui.View) error {
	volume, err := gui.Panels.Volumes.GetSelectedItem()
	if err != nil {
		return nil
	}

	type removeVolumeOption struct {
		description string
		command     string
		force       bool
	}

	options := []*removeVolumeOption{
		{
			description: gui.Tr.Remove,
			command:     utils.WithShortSha("docker volume rm " + volume.Name),
			force:       false,
		},
		{
			description: gui.Tr.ForceRemove,
			command:     utils.WithShortSha("docker volume rm --force " + volume.Name),
			force:       true,
		},
	}

	menuItems := lo.Map(options, func(option *removeVolumeOption, _ int) *types.MenuItem {
		return &types.MenuItem{
			LabelColumns: []string{option.description, color.New(color.FgRed).Sprint(option.command)},
			OnPress: func() error {
				return gui.WithWaitingStatus(gui.Tr.RemovingStatus, func() error {
					if err := volume.Remove(option.force); err != nil {
						return gui.createErrorPanel(err.Error())
					}
					return nil
				})
			},
		}
	})

	return gui.Menu(CreateMenuOptions{
		Title: "",
		Items: menuItems,
	})
}

func (gui *Gui) handlePruneVolumes() error {
	return gui.createConfirmationPanel(gui.Tr.Confirm, gui.Tr.ConfirmPruneVolumes, func(g *gocui.Gui, v *gocui.View) error {
		return gui.WithWaitingStatus(gui.Tr.PruningStatus, func() error {
			err := gui.DockerCommand.PruneVolumes()
			if err != nil {
				return gui.createErrorPanel(err.Error())
			}
			return nil
		})
	}, nil)
}

func (gui *Gui) handleVolumesCustomCommand(g *gocui.Gui, v *gocui.View) error {
	volume, err := gui.Panels.Volumes.GetSelectedItem()
	if err != nil {
		return nil
	}

	commandObject := gui.DockerCommand.NewCommandObject(commands.CommandObject{
		Volume: volume,
	})

	customCommands := gui.Config.UserConfig.CustomCommands.Volumes

	return gui.createCustomCommandMenu(customCommands, commandObject)
}

func (gui *Gui) handleVolumesBulkCommand(g *gocui.Gui, v *gocui.View) error {
	baseBulkCommands := []config.CustomCommand{
		{
			Name:             gui.Tr.PruneVolumes,
			InternalFunction: gui.handlePruneVolumes,
		},
	}

	bulkCommands := append(baseBulkCommands, gui.Config.UserConfig.BulkCommands.Volumes...)
	commandObject := gui.DockerCommand.NewCommandObject(commands.CommandObject{})

	return gui.createBulkCommandMenu(bulkCommands, commandObject)
}

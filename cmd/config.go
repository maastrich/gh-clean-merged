package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/maastrich/gh-clean-merged/internal/config"
	"github.com/maastrich/gh-clean-merged/internal/git"
	"github.com/maastrich/gh-clean-merged/internal/ui"
	"github.com/spf13/cobra"
)

// global selects the file every config subcommand works on: the repository's
// own file by default, the user wide one with -g.
var global bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and write the configuration files",
	Long: `Read and write the configuration files.

Two files are read, both optional: a global one under the user's config
directory, and a local one at the root of the repository. The local file wins
over the global one, except for protected patterns, which add up. Flags typed on
the command line win over both.

Subcommands work on the local file unless -g is passed.`,
	SilenceUsage: true,
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the configuration as it resolves, and where it comes from",
	Args:  cobra.NoArgs,
	RunE:  runConfigList,
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one value from the resolved configuration",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigGet,
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>...",
	Short: "Set one key, replacing what was there",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runConfigSet,
}

var configAddCmd = &cobra.Command{
	Use:   "add <key> <value>...",
	Short: "Append to a list key, such as protected",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runConfigAdd,
}

var configRemoveCmd = &cobra.Command{
	Use:   "remove <key> <value>...",
	Short: "Drop values from a list key",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runConfigRemove,
}

var configUnsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Remove one key, letting the next file up decide again",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigUnset,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the path of the file being edited",
	Args:  cobra.NoArgs,
	RunE:  runConfigPath,
}

func init() {
	configCmd.PersistentFlags().BoolVarP(&global, "global", "g", false, "Work on the global file instead of the repository's own")
	configCmd.AddCommand(configListCmd, configGetCmd, configSetCmd, configAddCmd, configRemoveCmd, configUnsetCmd, configPathCmd)
	rootCmd.AddCommand(configCmd)

	for _, cmd := range []*cobra.Command{configGetCmd, configSetCmd, configAddCmd, configRemoveCmd, configUnsetCmd} {
		cmd.Long = cmd.Short + ".\n\nKeys: " + strings.Join(config.Keys(), ", ")
	}
}

// target is the file the editing subcommands work on.
func target() (string, error) {
	if global {
		path := config.GlobalPath()
		if path == "" {
			return "", fmt.Errorf("could not determine the global config path, set XDG_CONFIG_HOME")
		}
		return path, nil
	}

	root, err := git.Root()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository, pass -g to edit the global config")
	}
	return config.LocalPath(root), nil
}

func runConfigList(cmd *cobra.Command, args []string) error {
	out := ui.New(os.Stdout, colorMode)

	root, _ := git.Root()
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}

	if len(cfg.Sources) == 0 {
		out.Printf("%s\n", out.Dim("No configuration file."))
	}
	for _, source := range cfg.Sources {
		out.Printf("%s %s\n", out.Bold("From"), out.Cyan(source))
	}
	out.Println()

	rows := make([]ui.Row, 0, len(config.Keys()))
	for _, name := range config.Keys() {
		value, set, err := config.Get(cfg.File, name)
		if err != nil {
			return err
		}
		if !set {
			value = "(unset)"
		}
		rows = append(rows, ui.Row{Marker: " ", Name: name, Reason: value, Paint: out.Blue})
	}
	out.Section("Configuration", rows)
	return nil
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	root, _ := git.Root()
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}

	value, set, err := config.Get(cfg.File, args[0])
	if err != nil {
		return err
	}
	if !set {
		return fmt.Errorf("%s is not set", args[0])
	}
	fmt.Println(value)
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	return edit(args[0], func(file *config.File) error {
		return config.Set(file, args[0], args[1:])
	})
}

func runConfigAdd(cmd *cobra.Command, args []string) error {
	return edit(args[0], func(file *config.File) error {
		return config.Add(file, args[0], args[1:])
	})
}

func runConfigRemove(cmd *cobra.Command, args []string) error {
	return edit(args[0], func(file *config.File) error {
		return config.Remove(file, args[0], args[1:])
	})
}

func runConfigUnset(cmd *cobra.Command, args []string) error {
	return edit(args[0], func(file *config.File) error {
		return config.Unset(file, args[0])
	})
}

func runConfigPath(cmd *cobra.Command, args []string) error {
	path, err := target()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// edit reads the targeted file, applies the change and writes it back, then
// reports what the key now holds in that file.
func edit(name string, apply func(*config.File) error) error {
	out := ui.New(os.Stdout, colorMode)

	path, err := target()
	if err != nil {
		return err
	}
	file, err := config.Read(path)
	if err != nil {
		return err
	}
	if err := apply(&file); err != nil {
		return err
	}
	if err := config.Save(path, file); err != nil {
		return err
	}

	value, set, err := config.Get(file, name)
	if err != nil {
		return err
	}
	if !set {
		value = "(unset)"
	}
	out.Printf("%s %s %s  %s\n", out.Green("wrote"), out.Cyan(path), out.Blue(name), out.Dim(value))
	return nil
}

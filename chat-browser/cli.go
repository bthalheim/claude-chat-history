package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var version = "dev"

// defaultDirs are searched, in order, in the current working directory.
var defaultDirs = []string{".chat-history", "chat-history"}

type options struct {
	dir     string // saved chat history directory
	project string // project whose local store receives restored sessions
	store   string // root of the local Claude store (default ~/.claude/projects)
}

// resolveDir returns the history directory: an explicit path if given,
// otherwise the first of defaultDirs that exists under the current directory.
func resolveDir(explicit string) (string, error) {
	if explicit != "" {
		st, err := os.Stat(explicit)
		if err != nil {
			return "", err
		}
		if !st.IsDir() {
			return "", fmt.Errorf("%s is not a directory", explicit)
		}
		return filepath.Abs(explicit)
	}
	for _, c := range defaultDirs {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return filepath.Abs(c)
		}
	}
	wd, _ := os.Getwd()
	return "", fmt.Errorf("no %s or %s folder in %s (use --dir to specify one)",
		defaultDirs[0], defaultDirs[1], wd)
}

// setup resolves the history directory and the local store.
func (o options) setup(positional string) (string, store, error) {
	path := o.dir
	if positional != "" {
		if path != "" && path != positional {
			return "", store{}, fmt.Errorf("both --dir and a positional path were given")
		}
		path = positional
	}
	dir, err := resolveDir(path)
	if err != nil {
		return "", store{}, err
	}
	st, err := newStore(o.store, o.project)
	return dir, st, err
}

func newRootCmd() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "chat-browser [dir]",
		Short: "Browse and restore Claude Code chat history",
		Long: `Browse Claude Code chat history in a terminal UI, and restore saved
sessions into the local Claude Code store so they can be resumed.

Without arguments, looks for .chat-history or chat-history in the current
directory. Pass a directory as an argument or with --dir to use another one.

Restored sessions go to the local store folder for the current directory
(or --project), under ~/.claude/projects (or --store).`,
		Args:          cobra.MaximumNArgs(1),
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var pos string
			if len(args) == 1 {
				pos = args[0]
			}
			dir, st, err := opts.setup(pos)
			if err != nil {
				return err
			}
			_, err = tea.NewProgram(newModel(dir, st), tea.WithAltScreen()).Run()
			return err
		},
	}
	pf := cmd.PersistentFlags()
	pf.StringVarP(&opts.dir, "dir", "d", "", "path to the saved chat history directory")
	pf.StringVar(&opts.project, "project", "", "project directory whose local store receives restores (default: current directory)")
	pf.StringVar(&opts.store, "store", "", "root of the local Claude store (default: ~/.claude/projects)")
	cmd.AddCommand(newStatusCmd(&opts), newRestoreCmd(&opts))
	return cmd
}

func sessionItems(dir string) ([]item, error) {
	all, err := loadItems(dir)
	if err != nil {
		return nil, err
	}
	var out []item
	for _, it := range all {
		if !it.isMD {
			out = append(out, it)
		}
	}
	return out, nil
}

func newStatusCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show how saved sessions compare with the local store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, st, err := opts.setup("")
			if err != nil {
				return err
			}
			items, err := sessionItems(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "saved: %s\nlocal: %s\n\n", dir, st.projectDir)
			for _, it := range items {
				s, err := compareFiles(it.path, st.localPath(it.id))
				if err != nil {
					s = stUnknown
				}
				fmt.Fprintf(out, "%-36s  %-8s  %s\n", it.id, s.label(), it.title)
			}
			return nil
		},
	}
}

func newRestoreCmd(opts *options) *cobra.Command {
	var all, force bool
	cmd := &cobra.Command{
		Use:   "restore [session-id-prefix...]",
		Short: "Restore saved sessions into the local store (create or bring up to date)",
		Long: `Restore saved sessions into the local Claude Code store.

A session that is missing locally is created. A session whose local copy is
an older prefix of the saved one is brought up to date. If the local copy has
content the saved one lacks (local+) or the two have diverged, nothing is
changed unless --force is given, in which case the local file is backed up
first.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return fmt.Errorf("give one or more session ids, or --all")
			}
			dir, st, err := opts.setup("")
			if err != nil {
				return err
			}
			items, err := sessionItems(dir)
			if err != nil {
				return err
			}
			var targets []item
			if all {
				targets = items
			}
			for _, a := range args {
				var match []item
				for _, it := range items {
					if strings.HasPrefix(it.id, a) {
						match = append(match, it)
					}
				}
				if len(match) != 1 {
					return fmt.Errorf("%q matches %d saved sessions", a, len(match))
				}
				targets = append(targets, match[0])
			}
			out := cmd.OutOrStdout()
			failed := 0
			for _, it := range targets {
				res, err := restoreFile(it.path, st.localPath(it.id), force)
				if err != nil {
					failed++
					fmt.Fprintf(out, "%s  skipped: %v\n", it.id, err)
					continue
				}
				fmt.Fprintf(out, "%s  %s\n", it.id, res)
			}
			if failed > 0 {
				return fmt.Errorf("%d session(s) not restored", failed)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "restore every saved session")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite local copies that are newer or diverged (backup kept)")
	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "chat-browser:", err)
		os.Exit(1)
	}
}

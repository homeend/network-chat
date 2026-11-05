package cli

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"netchat/internal/skills"
)

// netchat skills [list|install|remove] [agent...|all|detected]
// Works without the service running (writes files in the user's home).
func skillsCmd(args []string) error {
	sub := "list"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		sub, args = args[0], args[1:]
	}
	f := newFlags("skills " + sub)
	if _, err := f.parse(args); err != nil {
		return err
	}
	m, err := skills.NewManager()
	if err != nil {
		return err
	}
	ov := m.Overview()

	targets := func() ([]string, error) {
		var ids []string
		for _, a := range f.Args() {
			switch a {
			case "all":
				for _, s := range ov.Agents {
					ids = append(ids, s.ID)
				}
			case "detected":
				for _, s := range ov.Agents {
					if s.Detected {
						ids = append(ids, s.ID)
					}
				}
			default:
				ids = append(ids, a)
			}
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("usage: netchat skills %s <agent...|detected|all>", sub)
		}
		return ids, nil
	}

	switch sub {
	case "list", "ls":
		if *f.json {
			return printJSON(os.Stdout, ov)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "AGENT\tID\tDETECTED\tSKILL\tPATH")
		for _, s := range ov.Agents {
			det := "no"
			if s.Detected {
				det = "yes"
			}
			st := s.Status
			if s.InstalledVersion != "" {
				st += " v" + s.InstalledVersion
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Name, s.ID, det, st, s.Path)
		}
		return tw.Flush()
	case "install", "update":
		ids, err := targets()
		if err != nil {
			return err
		}
		var errs []error
		for _, id := range ids {
			if st, err := m.Install(id); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			} else {
				fmt.Printf("installed %s -> %s\n", id, st.Path)
			}
		}
		return errors.Join(errs...)
	case "remove", "rm", "uninstall":
		ids, err := targets()
		if err != nil {
			return err
		}
		var errs []error
		for _, id := range ids {
			if st, err := m.Remove(id); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			} else {
				fmt.Printf("removed %s (%s)\n", id, st.Path)
			}
		}
		return errors.Join(errs...)
	}
	return fmt.Errorf("unknown: netchat skills %s (list | install | remove)", sub)
}

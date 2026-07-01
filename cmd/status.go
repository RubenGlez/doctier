package cmd

import (
	"fmt"
	"sort"
	"text/tabwriter"
	"os"

	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// runStatus prints the effective classification of each listed document.
func runStatus(args []string) error {
	m, _, err := loadManifest()
	if err != nil {
		return err
	}
	files, err := gitx.ListFiles()
	if err != nil {
		return err
	}
	sort.Strings(files)

	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "DOCUMENT\tVISIBILITY\tLIFETIME\tSTORAGE\tEXPIRES")
	for _, f := range files {
		rule, ok := m.Match(f)
		if !ok {
			fmt.Fprintf(w, "%s\t—\t—\t(uncovered)\t—\n", f)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			f, rule.Visibility, rule.Lifetime, storage(rule), expiry(rule))
	}
	return w.Flush()
}

func storage(r config.Rule) string {
	switch {
	case r.LocalOnly():
		return "local (gitignored)"
	case r.Encrypted():
		return "git (encrypted)"
	default:
		return "git (plaintext)"
	}
}

func expiry(r config.Rule) string {
	if r.Expire == nil {
		return "—"
	}
	if r.Expire.On == "ttl" {
		return fmt.Sprintf("ttl %dd", r.Expire.TTLDays)
	}
	return r.Expire.On
}

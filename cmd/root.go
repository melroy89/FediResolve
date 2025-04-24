package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gitlab.melroy.org/melroy/fediresolve/resolver"
)

const Version = "1.0"

var versionFlag bool

var rootCmd = &cobra.Command{
	Use:   "fediresolve [url|handle]",
	Short: "Resolve and display Fediverse content (v" + Version + ")",
	Long: `Fediresolve is a CLI tool that resolves Fediverse URLs and handles.

It can parse and display content from Mastodon, Mbin, Lemmy, and other Fediverse platforms.
The tool supports both direct URLs to posts/comments/threads and Fediverse handles like @username@server.com.`,
	Run: func(cmd *cobra.Command, args []string) {
		if versionFlag {
			fmt.Println("fediresolve version", Version)
			os.Exit(0)
		}
		var input string

		if len(args) > 0 {
			input = args[0]
		} else {
			fmt.Print("Enter a Fediverse URL or handle: ")
			reader := bufio.NewReader(os.Stdin)
			input, _ = reader.ReadString('\n')
			input = strings.TrimSpace(input)
		}

		if input == "" {
			fmt.Println("No URL or handle provided. Exiting.")
			return
		}

		r := resolver.NewResolver()
		result, err := r.Resolve(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving %s: %v\n", input, err)
			os.Exit(1)
		}

		fmt.Println(result)
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&versionFlag, "version", false, "Print the version number and exit")
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

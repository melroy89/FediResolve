package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gitlab.melroy.org/melroy/fediresolve/resolver"
)

var rootCmd = &cobra.Command{
	Use:   "fediresolve [url]",
	Short: "Resolve and display Fediverse content",
	Long: `Fediresolve is a CLI tool that resolves Fediverse URLs and handles.
It can parse and display content from Mastodon, Lemmy, and other Fediverse platforms.
The tool supports both direct URLs to posts/comments/threads and Fediverse handles like @username@server.com.`,
	Run: func(cmd *cobra.Command, args []string) {
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

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return rootCmd.Execute()
}

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianevanmiller/beadcrumbs/internal/beads"
	"github.com/brianevanmiller/beadcrumbs/internal/slack"
	"github.com/brianevanmiller/beadcrumbs/internal/store"
	"github.com/spf13/cobra"
)

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Manage Slack integration",
	Long: `Commands for integrating with Slack workspaces.

Connect a Slack workspace to import channel messages as beadcrumbs insights.
Each project can connect to a different workspace via per-project config.

Setup:
  1. Create a Slack app at https://api.slack.com/apps
  2. Configure client ID and secret: bdc slack config client_id <value>
  3. Run OAuth flow: bdc slack auth
  4. Fetch messages: bdc slack fetch <channel>`,
}

var slackAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with Slack via OAuth",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		clientID, _ := s.GetConfig("slack.client_id")
		clientSecret, _ := s.GetConfig("slack.client_secret")

		if clientID == "" || clientSecret == "" {
			return fmt.Errorf("Slack app credentials not configured.\n\nSet them first:\n  bdc slack config client_id <your-client-id>\n  bdc slack config client_secret <your-client-secret>\n\nSee: bdc slack --help")
		}

		config := slack.OAuthConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes: []string{
				"channels:history",
				"channels:read",
				"groups:history",
				"groups:read",
				"im:history",
				"im:read",
				"users:read",
			},
		}

		result, err := slack.RunOAuthFlow(config)
		if err != nil {
			return fmt.Errorf("OAuth flow failed: %w", err)
		}

		// Store credentials
		if err := s.SetConfig("slack.bot_token", result.BotToken); err != nil {
			return fmt.Errorf("failed to save bot token: %w", err)
		}
		if err := s.SetConfig("slack.workspace", result.TeamID); err != nil {
			return fmt.Errorf("failed to save workspace ID: %w", err)
		}
		if err := s.SetConfig("slack.workspace_name", result.TeamName); err != nil {
			return fmt.Errorf("failed to save workspace name: %w", err)
		}

		fmt.Printf("Authenticated with workspace: %s (%s)\n", result.TeamName, result.TeamID)
		fmt.Println("Bot token stored in project config.")
		return nil
	},
}

var slackStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Slack integration status",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		botToken, _ := s.GetConfig("slack.bot_token")
		workspace, _ := s.GetConfig("slack.workspace")
		workspaceName, _ := s.GetConfig("slack.workspace_name")
		clientID, _ := s.GetConfig("slack.client_id")

		fmt.Println("Slack Integration Status")
		fmt.Println("────────────────────────")

		if clientID != "" {
			fmt.Printf("Client ID: %s...%s\n", clientID[:4], clientID[len(clientID)-4:])
		} else {
			fmt.Println("Client ID: (not set)")
		}

		if botToken == "" {
			fmt.Println("Bot token: (not authenticated)")
			fmt.Println("\nRun 'bdc slack auth' to connect a workspace.")
			return nil
		}

		if len(botToken) > 12 {
			fmt.Printf("Bot token: %s...%s\n", botToken[:8], botToken[len(botToken)-4:])
		}
		if workspaceName != "" {
			fmt.Printf("Workspace: %s (%s)\n", workspaceName, workspace)
		}

		// Test token validity
		client := slack.NewClient(botToken)
		authResp, err := client.AuthTest()
		if err != nil {
			fmt.Printf("Token status: invalid (%s)\n", err)
		} else {
			fmt.Printf("Token status: valid (bot: %s)\n", authResp.User)
		}

		return nil
	},
}

var slackChannelsCmd = &cobra.Command{
	Use:   "channels",
	Short: "List accessible Slack channels",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getSlackClient()
		if err != nil {
			return err
		}

		channels, err := client.ListChannels()
		if err != nil {
			return fmt.Errorf("failed to list channels: %w", err)
		}

		if len(channels) == 0 {
			fmt.Println("No accessible channels found.")
			return nil
		}

		fmt.Printf("Found %d channels:\n\n", len(channels))
		for _, ch := range channels {
			chType := "channel"
			if ch.IsGroup {
				chType = "group"
			} else if ch.IsIM {
				chType = "dm"
			} else if ch.IsMPIM {
				chType = "group-dm"
			}

			name := ch.Name
			if name == "" {
				name = ch.ID
			}
			fmt.Printf("  %-30s %s  (%s)\n", name, ch.ID, chType)
		}
		return nil
	},
}

var (
	slackFetchSince  string
	slackFetchUntil  string
	slackFetchThread string
	slackFetchDryRun bool
)

var slackFetchCmd = &cobra.Command{
	Use:   "fetch <channel-name-or-id>",
	Short: "Fetch messages from a Slack channel",
	Long: `Fetch messages from a Slack channel and import them as insights.

Channel can be specified as a name (without #) or a channel ID.

Examples:
  bdc slack fetch general                        # Fetch from #general
  bdc slack fetch C0123456789                    # Fetch by channel ID
  bdc slack fetch general --since=2024-01-01     # Since a date
  bdc slack fetch general --thread=thr-xxx       # Add to thread
  bdc slack fetch general --dry-run              # Preview only`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		channelRef := args[0]

		client, err := getSlackClient()
		if err != nil {
			return err
		}

		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		// Resolve channel name to ID
		channelID, channelName, err := resolveChannel(client, channelRef)
		if err != nil {
			return err
		}

		// Parse time bounds
		var since, until time.Time
		if slackFetchSince != "" {
			since, err = parseImportTimestamp(slackFetchSince)
			if err != nil {
				return fmt.Errorf("invalid --since: %w", err)
			}
		}
		if slackFetchUntil != "" {
			until, err = parseImportTimestamp(slackFetchUntil)
			if err != nil {
				return fmt.Errorf("invalid --until: %w", err)
			}
		}

		fmt.Printf("Fetching messages from #%s...\n", channelName)

		messages, err := client.FetchHistory(channelID, since, until)
		if err != nil {
			return fmt.Errorf("failed to fetch history: %w", err)
		}

		if len(messages) == 0 {
			fmt.Println("No messages found in the specified range.")
			return nil
		}

		// Set up user cache
		userCache := slack.NewUserCache(client, s)

		// Convert to insights
		insights := slack.ConvertMessages(messages, slack.ConvertOptions{
			ChannelName: channelName,
			UserCache:   userCache,
		})

		if len(insights) == 0 {
			fmt.Printf("Found %d messages but none qualified as insights (too short or noise).\n", len(messages))
			return nil
		}

		// Set thread if specified
		if slackFetchThread != "" {
			for _, insight := range insights {
				insight.ThreadID = slackFetchThread
			}
		}

		// Preview
		fmt.Printf("\nExtracted %d insights from %d messages:\n\n", len(insights), len(messages))
		for i, insight := range insights {
			symbol := getInsightSymbol(insight.Type)
			author := ""
			if insight.AuthorID != "" {
				author = fmt.Sprintf(" (%s)", insight.AuthorID)
			}
			fmt.Printf("  %d. %s \"%s\" [%s]%s\n", i+1, symbol, truncateContent(insight.Content, 60), insight.Type, author)
		}
		fmt.Println()

		if slackFetchDryRun {
			fmt.Println("Dry run - no insights saved.")
			return nil
		}

		// Verify thread exists if specified
		if slackFetchThread != "" {
			_, err := s.GetThread(slackFetchThread)
			if err != nil {
				return fmt.Errorf("thread %s not found: %w", slackFetchThread, err)
			}
		}

		// Save insights
		saved := 0
		for _, insight := range insights {
			if err := s.CreateInsight(insight); err != nil {
				fmt.Printf("Warning: failed to save insight: %v\n", err)
				continue
			}
			saved++
		}

		fmt.Printf("Saved %d insights.\n", saved)
		return nil
	},
}

var slackConfigCmd = &cobra.Command{
	Use:   "config <key> [value]",
	Short: "Get or set Slack integration config",
	Long: `Get or set Slack integration configuration.

Keys:
  client_id       Slack app client ID
  client_secret   Slack app client secret
  bot_token       Bot OAuth token (set automatically by 'bdc slack auth')
  workspace       Workspace ID (set automatically)
  workspace_name  Workspace name (set automatically)`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := getStore()
		if err != nil {
			return err
		}
		defer closeStore()

		key := "slack." + args[0]

		if len(args) == 1 {
			// Get
			value, err := s.GetConfig(key)
			if err != nil {
				return err
			}
			if value == "" {
				fmt.Printf("%s: (not set)\n", args[0])
			} else {
				// Mask secrets
				if (args[0] == "client_secret" || args[0] == "bot_token") && len(value) > 12 {
					fmt.Printf("%s: %s...%s\n", args[0], value[:8], value[len(value)-4:])
				} else {
					fmt.Printf("%s: %s\n", args[0], value)
				}
			}
		} else {
			// Set
			if err := s.SetConfig(key, args[1]); err != nil {
				return err
			}
			if args[0] == "client_secret" || args[0] == "bot_token" {
				fmt.Printf("Set %s\n", args[0])
			} else {
				fmt.Printf("Set %s = %s\n", args[0], args[1])
			}
		}

		return nil
	},
}

func init() {
	slackFetchCmd.Flags().StringVar(&slackFetchSince, "since", "", "fetch messages since this time (RFC3339 or date)")
	slackFetchCmd.Flags().StringVar(&slackFetchUntil, "until", "", "fetch messages until this time (RFC3339 or date)")
	slackFetchCmd.Flags().StringVar(&slackFetchThread, "thread", "", "add imported insights to this thread")
	slackFetchCmd.Flags().BoolVar(&slackFetchDryRun, "dry-run", false, "preview without saving")

	rootCmd.AddCommand(slackCmd)
	slackCmd.AddCommand(slackAuthCmd)
	slackCmd.AddCommand(slackStatusCmd)
	slackCmd.AddCommand(slackChannelsCmd)
	slackCmd.AddCommand(slackFetchCmd)
	slackCmd.AddCommand(slackConfigCmd)
}

// getSlackClient creates a Slack API client from stored config.
func getSlackClient() (*slack.Client, error) {
	s, err := getStore()
	if err != nil {
		return nil, err
	}

	botToken, _ := s.GetConfig("slack.bot_token")
	if botToken == "" {
		return nil, fmt.Errorf("not authenticated with Slack.\n\nRun 'bdc slack auth' to connect a workspace.")
	}

	return slack.NewClient(botToken), nil
}

// resolveChannel maps a channel name or ID to (channelID, channelName).
func resolveChannel(client *slack.Client, ref string) (string, string, error) {
	// If it looks like a channel ID (starts with C, D, or G + uppercase/digits)
	if len(ref) > 1 && (ref[0] == 'C' || ref[0] == 'D' || ref[0] == 'G') && isUpperAlphaNum(ref[1:]) {
		// Treat as channel ID — use it directly, name is the ref
		return ref, ref, nil
	}

	// Strip leading # if present
	ref = strings.TrimPrefix(ref, "#")

	// Look up by name
	channels, err := client.ListChannels()
	if err != nil {
		return "", "", fmt.Errorf("failed to list channels: %w", err)
	}

	for _, ch := range channels {
		if strings.EqualFold(ch.Name, ref) {
			return ch.ID, ch.Name, nil
		}
	}

	return "", "", fmt.Errorf("channel not found: %s\n\nRun 'bdc slack channels' to see available channels.", ref)
}

func isUpperAlphaNum(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// storeAdapter wraps *store.Store to satisfy slack.ConfigStore.
var _ slack.ConfigStore = (*store.Store)(nil)
// Note: store.Store already has GetConfig/SetConfig matching the interface,
// so we verify at compile time via the line above. If this fails to compile,
// we need an explicit adapter.

// resolveSlackRef resolves a Slack external ref for linking.
func resolveSlackRef(ref string) (*beads.ExternalRef, error) {
	if !strings.Contains(ref, ":") {
		ref = "slack:" + ref
	}
	return beads.ParseExternalRef(ref)
}

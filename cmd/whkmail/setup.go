package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wkirschbaum/whkmail/internal/dirs"
)

func runSetup(ctx context.Context) error {
	fmt.Println()
	fmt.Println("  " + styleBanner.Render("  whkmail setup  "))
	fmt.Println()
	fmt.Println("  Let's get you connected to Gmail.")
	fmt.Println()

	if err := os.MkdirAll(dirs.ConfigDir(), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	step("Config directory", dirs.ConfigDir())

	credFile := dirs.ConfigDir() + "/credentials.json"
	if _, err := os.Stat(credFile); os.IsNotExist(err) {
		fmt.Println()
		printSetupInstructions(credFile)
		fmt.Printf("\n%s\n", styleMuted.Render("Press Enter once credentials.json is in place…"))
		_, _ = fmt.Scanln()
		if _, err := os.Stat(credFile); err != nil {
			return fmt.Errorf("%s\n  Expected: %s",
				styleErr.Render("credentials.json not found"),
				stylePath.Render(credFile),
			)
		}
	}
	step("credentials.json", "found")
	fmt.Println()

	if _, err := os.Stat(dirs.TokenFile()); os.IsNotExist(err) {
		if err := performOAuth(ctx, credFile); err != nil {
			return err
		}
		fmt.Println()
	} else {
		step("Authorization", "already authorized — run 'whkmail auth' to re-authorize")
		fmt.Println()
	}

	fmt.Printf("  %s\n", styleMuted.Render("Installing whkmaild system service…"))
	if err := installService(); err != nil {
		fmt.Printf("  %s %v\n", styleErr.Render("!"), err)
		fmt.Printf("  %s\n", styleMuted.Render("Start the daemon manually: whkmaild"))
	} else {
		step("whkmaild service", "enabled and running")
	}

	fmt.Println()
	fmt.Println(styleBox.Render(
		styleOK.Render("  You're all set.\n\n") +
			"  Run " + styleStep.Render("whkmail") + " to open your inbox.",
	))
	fmt.Println()

	return nil
}

func step(label, value string) {
	fmt.Printf("  %s  %s: %s\n", styleOK.Render("✓"), label, value)
}

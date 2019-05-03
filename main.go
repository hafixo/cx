package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"encoding/base64"

	"github.com/cloud66-oss/cloud66"
	"github.com/cloud66/cli"
	"github.com/getsentry/raven-go"
)

type Command struct {
	Name       string
	Build      func() cli.Command
	Run        func(c *cli.Context)
	Flags      []cli.Flag
	Short      string
	Long       string
	NeedsStack bool
	NeedsOrg   bool
}

const (
	redirectURL       = "urn:ietf:wg:oauth:2.0:oob"
	scope             = "public redeploy jobs users admin"
	clientTokenEnvVar = "CLOUD66_TOKEN"
)

var (
	client      cloud66.Client
	debugMode   bool   = false
	underTest   bool   = false
	VERSION     string = "dev"
	BUILD_DATE  string = ""
	profile     *Profile
	profilePath string
)

var commands = []*Command{
	cmdStacks,
	cmdLogin,
	cmdRedeploy,
	cmdOpen,
	cmdSettings,
	cmdEasyDeploy,
	cmdEnvVars,
	cmdLease,
	cmdRun,
	cmdTunnel,
	cmdServers,
	cmdSsh,
	cmdTail,
	cmdUpload,
	cmdDownload,
	cmdBackups,
	cmdContainers,
	cmdServices,
	cmdSnapshots,
	cmdFormations,
	cmdDatabases,
	cmdJobs,
	cmdHelpEnviron,
	cmdUpdate,
	cmdInfo,
	cmdTest,
	cmdUsers,
	cmdTemplates,
	cmdGateway,
	cmdProcesses,
	cmdRegisterServer,
	cmdVersion,
	cmdDumpToken,
	cmdConfig,
}

var (
	flagStack       *cloud66.Stack
	flagOrg         *cloud66.Account
	flagEnvironment string
)

func main() {
	// add aliases for commands
	commands = populateAliases(commands)

	raven.SetDSN("https://39c187859231424fb4865e90d42a29a3:cfbc35db1b954f04be995a3d0ec3fbae@sentry.io/153008")
	defer recoverPanic()

	app := cli.NewApp()

	cmds := []cli.Command{}
	cli.VersionPrinter = runVersion

	for _, cmd := range commands {

		cliCommand := cmd.Build()

		if cmd.Name == "" {
			printFatal("No Name is specified for %s", cmd)
		}

		cliCommand.Name = cmd.Name
		cliCommand.Usage = cmd.Short
		cliCommand.Description = cmd.Long
		cliCommand.Action = cmd.Run
		cliCommand.Flags = cmd.Flags

		if len(cliCommand.Subcommands) == 0 {
			if cmd.NeedsStack {
				cliCommand.Flags = append(cliCommand.Flags,
					cli.StringFlag{
						Name:  "stack,s",
						Usage: "full or partial stack name. This can be omitted if the current directory is a stack directory",
					}, cli.StringFlag{
						Name:  "environment,e",
						Usage: "full or partial environment name",
					})
			}
			if cmd.NeedsOrg {
				cliCommand.Flags = append(cliCommand.Flags,
					cli.StringFlag{
						Name:  "org",
						Usage: "full or partial organization name.",
					})
			}
		} else {
			for idx, sub := range cliCommand.Subcommands {
				if cmd.NeedsStack {
					sub.Flags = append(sub.Flags,
						cli.StringFlag{
							Name:  "stack,s",
							Usage: "full or partial stack name. This can be omitted if the current directory is a stack directory",
						}, cli.StringFlag{
							Name:  "environment,e",
							Usage: "full or partial environment name",
						})
				}
				if cmd.NeedsOrg {
					cliCommand.Flags = append(sub.Flags,
						cli.StringFlag{
							Name:  "org",
							Usage: "full or partial organization name.",
						})
				}

				cliCommand.Subcommands[idx].Flags = sub.Flags
			}
		}

		cmds = append(cmds, cliCommand)
	}

	app.Commands = cmds
	app.Name = "cx"
	app.Usage = "Cloud 66 Command line toolbelt (Detailed help: https://help.cloud66.com/maestro/quickstarts/using-cloud66-toolbelt.html)"
	app.Author = "Cloud 66"
	app.Email = "support@cloud66.com"
	app.Version = VERSION
	app.CommandNotFound = suggest
	app.Before = beforeCommand
	app.Action = doMain

	setGlobals(app)
	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func beforeCommand(c *cli.Context) error {
	profilePath = filepath.Join(cxHome(), "cxprofiles.json")
	profiles, err := ReadProfiles(profilePath)
	if err != nil {
		return err
	}

	profileName := c.GlobalString("profile")
	if profileName == "" {
		profileName = profiles.LastProfile
	}

	if profileName != profiles.LastProfile {
		// if profile is switched, clear the ssh cache
		err := clearSshKeyCache()
		if err != nil {
			return err
		}
	}

	debugMode = c.GlobalBool("debug")

	var command string
	if len(c.Args()) >= 1 {
		command = c.Args().First()
	}

	profile = profiles.Profiles[profileName]

	if profile == nil {
		return fmt.Errorf("no profile named %s found", profileName)
	}

	if (command != "version") && (command != "help") && (command != "update") && (command != "test") {
		initClients(c, true)
	}

	if command == "test" {
		initClients(c, false)
	}

	if (command != "update") && (VERSION != "dev") {
		defer backgroundRun()
	}

	return nil
}

func setGlobals(app *cli.App) {
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "profile",
			Usage: "switches between different Cloud 66 profiles (this is a cx client profile)",
			Value: "",
		},
		cli.BoolFlag{
			Name:   "debug",
			Usage:  "run in debug mode",
			EnvVar: "CXDEBUG",
		},
	}
}

func buildBasicCommand() cli.Command {
	return cli.Command{}
}

func doMain(c *cli.Context) {
	cli.ShowAppHelp(c)
}

func initClients(c *cli.Context, startAuth bool) {
	client = cloud66.GetClient(profile.BaseURL, cxHome(), profile.TokenFile, VERSION, "cx", profile.ClientID, profile.ClientSecret, redirectURL, scope)

	// check if cxHome exists and create it if not
	err := createDirIfNotExist(cxHome())
	if err != nil {
		fmt.Println("An error occurred trying create .cloud66 directory in HOME.")
		os.Exit(99)
	}
	tokenAbsolutePath := filepath.Join(cxHome(), profile.TokenFile)
	// is there a token file?
	_, err = os.Stat(tokenAbsolutePath)
	if err != nil {
		// are we running headless?
		tokenValue := os.Getenv(clientTokenEnvVar)
		// is there an env variable?
		if tokenValue != "" {
			err = writeClientToken(tokenAbsolutePath, tokenValue)
			if err != nil {
				printFatal("an error occurred trying to write environment variable as auth token.", err)
			}
		} else {
			fmt.Println("No previous authentication found.")
			if startAuth {
				client.Authorize(cxHome(), profile.TokenFile, profile.ClientID, profile.ClientSecret, redirectURL, scope)
				os.Exit(1)
			} else {
				os.Exit(1)
			}
		}
	}

	organization, err := org(c)
	if err != nil {
		printFatal("Unable to retrieve organization")
		os.Exit(2)
	}
	if organization != nil {
		client.AccountId = &organization.Id
	}
	debugMode = c.GlobalBool("debug")
	client.Debug = debugMode

}

// write environment variable as auth token:
func writeClientToken(tokenAbsolutePath, tokenValue string) error {
	// convert tokenValue to un-base64 value
	tokenValueDec, err := base64.StdEncoding.DecodeString(tokenValue)
	if err != nil {
		return err
	}
	// write the value to tokenFile unless there is an error
	err = ioutil.WriteFile(tokenAbsolutePath, tokenValueDec, 0600)
	if err != nil {
		return err
	}
	return nil
}

// create a directory if it doesn't exist
func createDirIfNotExist(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0700)
		if err != nil {
			return err
		}
	}
	return nil
}

func recoverPanic() {
	if VERSION != "dev" {
		raven.CapturePanicAndWait(func() {
			if rec := recover(); rec != nil {
				panic(rec)
			}
		}, map[string]string{
			"Version":      VERSION,
			"Platform":     runtime.GOOS,
			"Architecture": runtime.GOARCH,
			"goversion":    runtime.Version()})
	}
}

func filterByEnvironmentExact(item interface{}) bool {
	if flagEnvironment == "" {
		return true
	}
	return strings.ToLower(item.(cloud66.Stack).Environment) == strings.ToLower(flagEnvironment)
}

func filterByEnvironmentFuzzy(item interface{}) bool {
	if flagEnvironment == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(item.(cloud66.Stack).Environment), strings.ToLower(flagEnvironment))
}

func org(c *cli.Context) (*cloud66.Account, error) {
	if flagOrg != nil {
		return flagOrg, nil
	}

	if c.String("org") != "" || profile.Organization != "" {
		var orgToFind string
		if c.String("org") != "" {
			orgToFind = c.String("org")
		} else {
			orgToFind = profile.Organization
		}

		orgs, err := client.AccountInfos()
		if err != nil {
			return nil, err
		}

		var orgNames []string
		for _, org := range orgs {
			if org.Name == "" {
				return nil, errors.New("one or more of the organizations you are a member of doesn't have a name. Please make sure you name the organizations")
			}
			orgNames = append(orgNames, org.Name)
		}
		idx, err := fuzzyFind(orgNames, orgToFind, false)
		if err != nil {
			return nil, err
		}

		flagOrg = &orgs[idx]
	} else {
		flagOrg = nil
	}

	return flagOrg, nil
}

func stack(c *cli.Context) (*cloud66.Stack, error) {
	if flagStack != nil {
		return flagStack, nil
	}

	if c.String("environment") != "" {
		flagEnvironment = c.String("environment")
	}

	var err error
	if c.String("stack") != "" {
		stacks, err := client.StackListWithFilter(filterByEnvironmentExact)
		if err != nil {
			return nil, err
		}
		var stackNames []string
		for _, stack := range stacks {
			stackNames = append(stackNames, stack.Name)
		}
		idx, err := fuzzyFind(stackNames, c.String("stack"), false)
		if err != nil {
			// try fuzzy env match
			stacks, err = client.StackListWithFilter(filterByEnvironmentFuzzy)
			if err != nil {
				return nil, err
			}
			var stackFuzzNames []string
			for _, stack := range stacks {
				stackFuzzNames = append(stackFuzzNames, stack.Name)
			}
			idx, err = fuzzyFind(stackFuzzNames, c.String("stack"), false)
			if err != nil {
				return nil, err
			}
		}

		flagStack = &stacks[idx]

		// toSdout is of type []bool. Take first value
		if c.String("environment") != "" {
			fmt.Printf("(%s)\n", flagStack.Environment)
		}

		return flagStack, err
	}

	if stack := c.String("cxstack"); stack != "" {
		// the environment variable should be exact match
		flagStack, err = client.StackInfo(stack)
		return flagStack, err
	}

	return stackFromGitRemote(remoteGitUrl(), localGitBranch())
}

func mustStack(c *cli.Context) *cloud66.Stack {
	stack, err := stack(c)
	if err != nil {
		printFatal(err.Error())
	}

	if stack == nil {
		printFatal("No stack specified. Either use --stack flag to cd to a stack directory")
	}

	return stack
}

func mustServer(c *cli.Context, stack cloud66.Stack, flagServer string, ignoreDocker bool) *cloud66.Server {
	servers, err := client.Servers(stack.Uid)
	if err != nil {
		printFatal(err.Error())
	}
	server, err := findServer(servers, flagServer)
	if err != nil {
		printFatal(err.Error())
	}
	if server == nil {
		printFatal("Server '" + flagServer + "' not found")
	}
	if !ignoreDocker && !server.HasRole("docker") && !server.HasRole("kubes") {
		printFatal("Server '" + flagServer + "' is not a docker server")
	}
	fmt.Printf("Server: %s\n", server.Name)
	return server
}

func mustOrg(c *cli.Context) *cloud66.Account {
	org, err := org(c)
	if err != nil {
		printFatal(err.Error())
	}

	if org == nil {
		printFatal("No organization specified. Please use --org flag")
	}

	return org
}

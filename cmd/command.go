package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/loft-sh/devpod-provider-aws/pkg/aws"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/provider"
	"github.com/loft-sh/devpod/pkg/ssh"
	"github.com/spf13/cobra"
)

// CommandCmd holds the cmd flags
type CommandCmd struct{}

// NewCommandCmd defines a command
func NewCommandCmd() *cobra.Command {
	cmd := &CommandCmd{}
	commandCmd := &cobra.Command{
		Use:   "command",
		Short: "Command an instance",
		RunE: func(_ *cobra.Command, args []string) error {
			awsProvider, err := aws.NewProvider(context.Background(), log.Default)
			if err != nil {
				return err
			}

			return cmd.Run(
				context.Background(),
				awsProvider,
				provider.FromEnvironment(),
				log.Default,
			)
		},
	}

	return commandCmd
}

// Run runs the command logic
func (cmd *CommandCmd) Run(ctx context.Context, providerAws *aws.AwsProvider, machine *provider.Machine, logs log.Logger) error {
	command := os.Getenv("COMMAND")
	if command == "" {
		return fmt.Errorf("command environment variable is missing")
	}

	// get private key
	privateKey, err := ssh.GetPrivateKeyRawBase(providerAws.Config.MachineFolder)
	if err != nil {
		return fmt.Errorf("load private key: %w", err)
	}

	// get instance
	instance, err := aws.GetDevpodRunningInstance(
		ctx,
		providerAws.AwsConfig,
		providerAws.Config.MachineID,
	)
	if err != nil {
		return err
	} else if len(instance.Reservations) == 0 {
		return fmt.Errorf("instance %s doesn't exist", providerAws.Config.MachineID)
	}

	// try public ip
	if instance.Reservations[0].Instances[0].PublicIpAddress != nil {
		ip := *instance.Reservations[0].Instances[0].PublicIpAddress

		sshClient, err := ssh.NewSSHClient("devpod", ip+":22", privateKey)
		if err != nil {
			logs.Debugf("error connecting to public ip [%s]: %v", ip, err)
		} else {
			// successfully connected to the public ip
			defer sshClient.Close()

			return ssh.Run(ctx, sshClient, command, os.Stdin, os.Stdout, os.Stderr)
		}
	}

	// try private ip
	if instance.Reservations[0].Instances[0].PrivateIpAddress != nil {
		ip := *instance.Reservations[0].Instances[0].PrivateIpAddress

		sshClient, err := ssh.NewSSHClient("devpod", ip+":22", privateKey)
		if err != nil {
			logs.Debugf("error connecting to private ip [%s]: %v", ip, err)
		} else {
			// successfully connected to the private ip
			defer sshClient.Close()

			return ssh.Run(ctx, sshClient, command, os.Stdin, os.Stdout, os.Stderr)
		}
	}

	return fmt.Errorf(
		"instance %s is not reachable",
		providerAws.Config.MachineID,
	)
}

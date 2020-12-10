package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/datawire/telepresence2/pkg/client"
	"github.com/datawire/telepresence2/pkg/rpc/connector"
	"github.com/datawire/telepresence2/pkg/rpc/manager"
)

type interceptInfo struct {
	sessionInfo
	name      string
	agentName string
	port      int
}

type interceptState struct {
	*interceptInfo
	cs *connectorState
}

func interceptCommand() *cobra.Command {
	ii := &interceptInfo{}
	cmd := &cobra.Command{
		Use:   "intercept [flags] <name> [-- command with arguments...]",
		Short: "Intercept a service",
		Args:  cobra.MinimumNArgs(1),
		RunE:  ii.intercept,
	}
	flags := cmd.Flags()
	ii.addConnectFlags(flags)

	flags.StringVarP(&ii.agentName, "deployment", "s", "", "Name of deployment to intercept, if different from <name>")
	flags.IntVarP(&ii.port, "port", "p", 8080, "Local port to forward to")

	return cmd
}

func leaveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "leave <name of intercept>",
		Short: "Remove existing intercept",
		Args:  cobra.ExactArgs(1),
		RunE:  removeIntercept,
	}
}

func listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List current intercepts",
		Args:  cobra.NoArgs,
		RunE:  listIntercepts,
	}
}

func (ii *interceptInfo) intercept(cmd *cobra.Command, args []string) error {
	ii.name = args[0]
	args = args[1:]
	if ii.agentName == "" {
		ii.agentName = ii.name
	}
	ii.cmd = cmd
	if len(args) == 0 {
		// start and retain the intercept
		return ii.withConnector(true, func(cs *connectorState) error {
			is := ii.newInterceptState(cs)
			return client.WithEnsuredState(is, true, func() error { return nil })
		})
	}

	// start intercept, run command, then stop the intercept
	return ii.withConnector(false, func(cs *connectorState) error {
		is := ii.newInterceptState(cs)
		return client.WithEnsuredState(is, false, func() error {
			return start(args[0], args[1:], true, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		})
	})
}

// listIntercepts requests a list current intercepts from the daemon
func listIntercepts(cmd *cobra.Command, _ []string) error {
	var r *manager.InterceptInfoSnapshot
	var err error
	err = withStartedConnector(cmd, func(cs *connectorState) error {
		r, err = cs.grpc.ListIntercepts(cmd.Context(), &empty.Empty{})
		return err
	})
	if err != nil {
		return err
	}
	stdout := cmd.OutOrStdout()
	if len(r.Intercepts) == 0 {
		fmt.Fprintln(stdout, "No intercepts")
		return nil
	}
	for idx, cept := range r.Intercepts {
		spec := cept.Spec
		fmt.Fprintf(stdout, "%4d. %s\n", idx+1, spec.Name)
		fmt.Fprintf(stdout, "      Intercepting requests and redirecting them to %s:%d\n", spec.TargetHost, spec.TargetPort)
		var previewURL string
		if cept.PreviewDomain != "" {
			previewURL = cept.PreviewDomain
			// Right now SystemA gives back domains with the leading "https://", but
			// let's not rely on that.
			if !strings.HasPrefix(previewURL, "https://") && !strings.HasPrefix(previewURL, "http://") {
				previewURL = "https://" + previewURL
			}
		}
		if previewURL != "" {
			fmt.Fprintln(stdout, "Share a preview of your changes with anyone by visiting\n  ", previewURL)
		}
	}
	return nil
}

// removeIntercept tells the daemon to deactivate and remove an existent intercept
func removeIntercept(cmd *cobra.Command, args []string) error {
	return withStartedConnector(cmd, func(cs *connectorState) error {
		is := &interceptInfo{name: strings.TrimSpace(args[0])}
		return is.newInterceptState(cs).DeactivateState()
	})
}

func (ii *interceptInfo) newInterceptState(cs *connectorState) *interceptState {
	return &interceptState{interceptInfo: ii, cs: cs}
}

func interceptMessage(ie connector.InterceptError, txt string) string {
	msg := ""
	switch ie {
	case connector.InterceptError_UNSPECIFIED:
	case connector.InterceptError_NO_PREVIEW_HOST:
		msg = `Your cluster is not configured for Preview URLs.
(Could not find a Host resource that enables Path-type Preview URLs.)
Please specify one or more header matches using --match.`
	case connector.InterceptError_NO_CONNECTION:
		msg = errConnectorIsNotRunning.Error()
	case connector.InterceptError_NO_TRAFFIC_MANAGER:
		msg = "Intercept unavailable: no traffic manager"
	case connector.InterceptError_TRAFFIC_MANAGER_CONNECTING:
		msg = "Connecting to traffic manager..."
	case connector.InterceptError_ALREADY_EXISTS:
		msg = fmt.Sprintf("Intercept with name %q already exists", txt)
	case connector.InterceptError_NO_ACCEPTABLE_DEPLOYMENT:
		msg = fmt.Sprintf("No interceptable deployment matching %s found", txt)
	case connector.InterceptError_TRAFFIC_MANAGER_ERROR:
		msg = txt
	case connector.InterceptError_AMBIGUOUS_MATCH:
		var matches []manager.AgentInfo
		err := json.Unmarshal([]byte(txt), &matches)
		if err != nil {
			msg = fmt.Sprintf("Unable to unmarshal JSON: %s", err.Error())
			break
		}
		st := &strings.Builder{}
		fmt.Fprintf(st, "Found more than one possible match:")
		for idx := range matches {
			match := &matches[idx]
			fmt.Fprintf(st, "\n%4d: %s on host %s", idx+1, match.Name, match.Hostname)
		}
		msg = st.String()
	case connector.InterceptError_FAILED_TO_ESTABLISH:
		msg = fmt.Sprintf("Failed to establish intercept: %s", txt)
	case connector.InterceptError_FAILED_TO_REMOVE:
		msg = fmt.Sprintf("Error while removing intercept: %v", txt)
	case connector.InterceptError_NOT_FOUND:
		msg = fmt.Sprintf("Intercept named %q not found", txt)
	}
	return msg
}

func (is *interceptState) EnsureState() (bool, error) {
	if is.name == "" {
		is.name = is.agentName
	}
	r, err := is.cs.grpc.CreateIntercept(is.cmd.Context(), &manager.CreateInterceptRequest{
		InterceptSpec: &manager.InterceptSpec{
			Name:       is.name,
			Agent:      is.agentName,
			Mechanism:  "tcp",
			TargetHost: "127.0.0.1",
			TargetPort: int32(is.port),
		},
	})
	if err != nil {
		return false, err
	}
	switch r.Error {
	case connector.InterceptError_UNSPECIFIED:
		fmt.Fprintf(is.cmd.OutOrStdout(), "Using deployment %s\n", is.agentName)
		return true, nil
	case connector.InterceptError_ALREADY_EXISTS:
		fmt.Fprintln(is.cmd.OutOrStdout(), interceptMessage(r.Error, r.ErrorText))
		return false, nil
	case connector.InterceptError_NO_CONNECTION:
		return false, errConnectorIsNotRunning
	}
	return false, errors.New(interceptMessage(r.Error, r.ErrorText))
}

func (is *interceptState) DeactivateState() error {
	name := strings.TrimSpace(is.name)
	var r *connector.InterceptResult
	var err error
	r, err = is.cs.grpc.RemoveIntercept(context.Background(), &manager.RemoveInterceptRequest2{Name: name})
	if err != nil {
		return err
	}
	if r.Error != connector.InterceptError_UNSPECIFIED {
		return errors.New(interceptMessage(r.Error, r.ErrorText))
	}
	return nil
}

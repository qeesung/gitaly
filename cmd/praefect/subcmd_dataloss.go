package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"time"

	"github.com/golang/protobuf/ptypes"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

var errFromNotBeforeTo = errors.New("'from' must be a time before 'to'")

type timeFlag time.Time

func (tf *timeFlag) String() string {
	return time.Time(*tf).Format(time.RFC3339)
}

func (tf *timeFlag) Set(v string) error {
	t, err := time.Parse(time.RFC3339, v)
	*tf = timeFlag(t)
	return err
}

type datalossSubcommand struct {
	output io.Writer
	from   time.Time
	to     time.Time
}

func newDatalossSubcommand() *datalossSubcommand {
	now := time.Now()
	return &datalossSubcommand{
		output: os.Stdout,
		from:   now.Add(-6 * time.Hour),
		to:     now,
	}
}

func (cmd *datalossSubcommand) FlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("dataloss", flag.ContinueOnError)
	fs.Var((*timeFlag)(&cmd.from), "from", "inclusive beginning of timerange")
	fs.Var((*timeFlag)(&cmd.to), "to", "exclusive ending of timerange")
	return fs
}

func (cmd *datalossSubcommand) Exec(_ *flag.FlagSet, cfg config.Config) error {
	nodeAddr, err := getNodeAddress(cfg)
	if err != nil {
		return err
	}

	if !cmd.from.Before(cmd.to) {
		return errFromNotBeforeTo
	}

	pbFrom, err := ptypes.TimestampProto(cmd.from)
	if err != nil {
		return fmt.Errorf("invalid 'from': %v", err)
	}

	pbTo, err := ptypes.TimestampProto(cmd.to)
	if err != nil {
		return fmt.Errorf("invalid 'to': %v", err)
	}

	conn, err := subCmdDial(nodeAddr, cfg.Auth.Token)
	if err != nil {
		return fmt.Errorf("error dialing: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Printf("error closing connection: %v", err)
		}
	}()

	client := gitalypb.NewPraefectInfoServiceClient(conn)
	resp, err := client.DatalossCheck(context.Background(), &gitalypb.DatalossCheckRequest{
		From: pbFrom,
		To:   pbTo,
	})
	if err != nil {
		return fmt.Errorf("error checking: %v", err)
	}

	keys := make([]string, 0, len(resp.ByRelativePath))
	for k := range resp.ByRelativePath {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if _, err := fmt.Fprintf(cmd.output, "Failed replication jobs between [%s, %s):\n", cmd.from, cmd.to); err != nil {
		return fmt.Errorf("error writing output: %v", err)
	}

	for _, proj := range keys {
		if _, err := fmt.Fprintf(cmd.output, "%s: %d jobs\n", proj, resp.ByRelativePath[proj]); err != nil {
			return fmt.Errorf("error writing output: %v", err)
		}
	}

	return nil
}

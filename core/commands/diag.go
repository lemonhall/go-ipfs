package commands

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"text/template"
	"time"

	cmds "github.com/jbenet/go-ipfs/commands"
	diag "github.com/jbenet/go-ipfs/diagnostics"
)

type DiagnosticConnection struct {
	ID string
	// TODO use milliseconds or microseconds for human readability
	NanosecondsLatency uint64
	Count              int
}

var (
	visD3   = "d3"
	visDot  = "dot"
	visFmts = []string{visD3, visDot}
)

type DiagnosticPeer struct {
	ID                string
	UptimeSeconds     uint64
	BandwidthBytesIn  uint64
	BandwidthBytesOut uint64
	Connections       []DiagnosticConnection
}

type DiagnosticOutput struct {
	Peers []DiagnosticPeer
}

var DefaultDiagnosticTimeout = time.Second * 20

var DiagCmd = &cmds.Command{
	Helptext: cmds.HelpText{
		Tagline: "Generates diagnostic reports",
	},

	Subcommands: map[string]*cmds.Command{
		"net": diagNetCmd,
	},
}

var diagNetCmd = &cmds.Command{
	Helptext: cmds.HelpText{
		Tagline: "Generates a network diagnostics report",
		ShortDescription: `
Sends out a message to each node in the network recursively
requesting a listing of data about them including number of
connected peers and latencies between them.

The given timeout will be decremented 2s at every network hop, 
ensuring peers try to return their diagnostics before the initiator's 
timeout. If the timeout is too small, some peers may not be reached.
30s and 60s are reasonable timeout values, though network vary.
The default timeout is 20 seconds.

The 'vis' option may be used to change the output format.
four formats are supported:
 * plain text - easy to read
 * d3 - json ready to be fed into d3view
 * dot - graphviz format

The d3 format will output a json object ready to be consumed by
the chord network viewer, available at the following hash:

    /ipfs/QmbesKpGyQGd5jtJFUGEB1ByPjNFpukhnKZDnkfxUiKn38

To view your diag output, 'ipfs add' the d3 vis output, and
open the following link:

	http://gateway.ipfs.io/ipfs/QmbesKpGyQGd5jtJFUGEB1ByPjNFpukhnKZDnkfxUiKn38/chord#<your hash>

The dot format can be fed into graphviz and other programs
that consume the dot format to generate graphs of the network.
`,
	},

	Options: []cmds.Option{
		cmds.StringOption("timeout", "diagnostic timeout duration"),
		cmds.StringOption("vis", "output vis. one of: "+strings.Join(visFmts, ", ")),
	},

	Run: func(req cmds.Request, res cmds.Response) {
		n, err := req.Context().GetNode()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}

		if !n.OnlineMode() {
			res.SetError(errNotOnline, cmds.ErrClient)
			return
		}

		vis, _, err := req.Option("vis").String()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}

		timeoutS, _, err := req.Option("timeout").String()
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}
		timeout := DefaultDiagnosticTimeout
		if timeoutS != "" {
			t, err := time.ParseDuration(timeoutS)
			if err != nil {
				res.SetError(errors.New("error parsing timeout"), cmds.ErrNormal)
				return
			}
			timeout = t
		}

		info, err := n.Diagnostics.GetDiagnostic(timeout)
		if err != nil {
			res.SetError(err, cmds.ErrNormal)
			return
		}

		switch vis {
		case visD3:
			res.SetOutput(bytes.NewReader(diag.GetGraphJson(info)))
		case visDot:
			var buf bytes.Buffer
			w := diag.DotWriter{W: &buf}
			err := w.WriteGraph(info)
			if err != nil {
				res.SetError(err, cmds.ErrNormal)
				return
			}
			res.SetOutput(io.Reader(&buf))
		default:
			output, err := stdDiagOutputMarshal(standardDiagOutput(info))
			if err != nil {
				res.SetError(err, cmds.ErrNormal)
				return
			}
			res.SetOutput(output)
		}
	},
}

func stdDiagOutputMarshal(output *DiagnosticOutput) (io.Reader, error) {
	var buf bytes.Buffer
	err := printDiagnostics(&buf, output)
	if err != nil {
		return nil, err
	}
	return &buf, nil
}

func standardDiagOutput(info []*diag.DiagInfo) *DiagnosticOutput {
	output := make([]DiagnosticPeer, len(info))
	for i, peer := range info {
		connections := make([]DiagnosticConnection, len(peer.Connections))
		for j, conn := range peer.Connections {
			connections[j] = DiagnosticConnection{
				ID:                 conn.ID,
				NanosecondsLatency: uint64(conn.Latency.Nanoseconds()),
				Count:              conn.Count,
			}
		}

		output[i] = DiagnosticPeer{
			ID:                peer.ID,
			UptimeSeconds:     uint64(peer.LifeSpan.Seconds()),
			BandwidthBytesIn:  peer.BwIn,
			BandwidthBytesOut: peer.BwOut,
			Connections:       connections,
		}
	}
	return &DiagnosticOutput{output}
}

func printDiagnostics(out io.Writer, info *DiagnosticOutput) error {
	diagTmpl := `
{{ range $peer := .Peers }}
ID {{ $peer.ID }} up {{ $peer.UptimeSeconds }} seconds connected to {{ len .Connections }}:{{ range $connection := .Connections }}
	ID {{ $connection.ID }} connections: {{ $connection.Count }} latency: {{ $connection.NanosecondsLatency }} ns{{ end }}
{{end}}
`

	templ, err := template.New("DiagnosticOutput").Parse(diagTmpl)
	if err != nil {
		return err
	}

	err = templ.Execute(out, info)
	if err != nil {
		return err
	}

	return nil
}

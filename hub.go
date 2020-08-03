package main

import (
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"

	"github.com/decred/dcrd/dcrutil/v3"
	"github.com/decred/dcrlnd/lnrpc"
	"github.com/decred/dcrlnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	macaroon "gopkg.in/macaroon.v2"
)

// lightningHub is a Decred Channel Hub. The main action for the hub is open
// more channels and help to increase the Decred's Lightning Network. The hub
// required a connection to a local lnd node in order to operate properly.
type lightningHub struct {
	lnd      lnrpc.LightningClient
	template *template.Template
	cfg      *config
	context  *templateContext
}

// templateContext defines the inital context required to rendering dcrlnhub.
type templateContext struct {
	NodeAddr        string
	Network         string
	ChannelsCount   uint32
	Capacity        int64
	Balance         dcrutil.Amount
	ActiveChannels  []*lnrpc.Channel
	DonationAddr    string
	DonationInvoice string
}

func newLightningHub(cfg *config, template *template.Template) (
	*lightningHub, error) {

	// First attempt to establish a connection to dcrlnd's RPC sever.
	tlsCertPath := cleanAndExpandPath(cfg.TLSCertPath)
	creds, err := credentials.NewClientTLSFromFile(tlsCertPath, "")
	if err != nil {
		return nil, fmt.Errorf("unable to read cert file: %v", err)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}

	// Load the specified macaroon file.
	macPath := cleanAndExpandPath(cfg.MacaroonPath)
	macBytes, err := ioutil.ReadFile(macPath)
	if err != nil {
		return nil, err
	}
	mac := &macaroon.Macaroon{}
	if err = mac.UnmarshalBinary(macBytes); err != nil {
		return nil, err
	}

	// Now we append the macaroon credentials to the dial options.
	opts = append(
		opts,
		grpc.WithPerRPCCredentials(macaroons.NewMacaroonCredential(mac)),
	)
	conn, err := grpc.Dial(cfg.RPCHost, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to dial to dcrlnd's gRPC server: %v", err)
	}

	// If we're able to connect out to the dcrlnd node, then we can start up
	// the hub safely.
	lnd := lnrpc.NewLightningClient(conn)

	// Get chain info to stop creation if the dcrlnd and dcrlnfaucet
	// are set in different networks.
	homeCtx, err := fetchHomePage(lnd, cfg)
	if err != nil {
		log.Errorf("%v", err)
		return nil, fmt.Errorf("unable to get initial info: %v", err)
	}

	return &lightningHub{
		lnd:      lnd,
		template: template,
		cfg:      cfg,
		context:  homeCtx,
	}, nil
}

// fetchHomePage query the information required and pass to the template context
// to be present in the Hub's home page.
func fetchHomePage(lnd lnrpc.LightningClient, cfg *config) (
	*templateContext, error) {

	// First query for the general information from the dcrlnd node, this'll
	// be used to populate the number of active channel as well as the
	// identity of the node.
	infoReq := &lnrpc.GetInfoRequest{}
	nodeInfo, err := lnd.GetInfo(ctxb, infoReq)
	if err != nil {
		return nil, fmt.Errorf("rpc GetInfo() failed: %v", err)
	}

	// Stop creation if the dcrlnd and dcrlnhub are set in different networks.
	activeNetwork := nodeInfo.Chains[0].Network
	if activeNetwork != cfg.Network {
		return nil, fmt.Errorf(
			"dcrlnd and dcrlnhub are set in different "+
				"networks <dcrlnd: %v / dcrlnhub: %v>",
			activeNetwork, cfg.Network)
	}

	// Get the dcrlnd's node uri.
	nodeAddr := ""
	if len(nodeInfo.Uris) == 0 {
		log.Warn("nodeInfo did not include a URI. external_ip config of dcrlnd is probably not set")
	} else {
		nodeAddr = nodeInfo.Uris[0]
	}

	// Get active channels list.
	listChanReq := &lnrpc.ListChannelsRequest{}
	listChanRes, err := lnd.ListChannels(ctxb, listChanReq)
	if err != nil {
		return nil, fmt.Errorf("rpc ListChannels() failed: %v", err)
	}

	// With channels list now we'll calculete the total capacity in atoms.
	var totalCapacity int64
	for _, channel := range listChanRes.Channels {
		totalCapacity += channel.Capacity
	}

	// Get the on-chain wallet balance.
	walletBalanceReq := &lnrpc.WalletBalanceRequest{}
	walletBalanceRes, err := lnd.WalletBalance(ctxb, walletBalanceReq)
	if err != nil {
		return nil, fmt.Errorf("rpc WalletBalance() failed: %v", err)
	}
	log.Warn(nodeInfo.NumActiveChannels)
	return &templateContext{
		NodeAddr:       nodeAddr,
		Network:        activeNetwork,
		ChannelsCount:  nodeInfo.NumActiveChannels,
		Capacity:       totalCapacity,
		Balance:        dcrutil.Amount(walletBalanceRes.ConfirmedBalance),
		ActiveChannels: listChanRes.Channels,
	}, nil
}

// HomePage renders the home page for the hub.
//
// NOTE: This method implements the http.Handler interface.
func (h *lightningHub) HomePage(w http.ResponseWriter, r *http.Request) {

	// First obtain the home template from our cache of pre-compiled
	// templates.
	homeTemplate := h.template.Lookup("index.html")
	if homeTemplate == nil {
		log.Error("unable to lookup index")
		http.Error(w, "500 Internal Server Error.", http.StatusInternalServerError)
		return
	}

	// In order to render the home template we'll need the necessary
	// context, so we'll grab that from the lnd daemon now in order to get
	// the most up to date state.
	homeInfo, err := fetchHomePage(h.lnd, h.cfg)
	if err != nil {
		log.Error("unable to fetch home state")
		http.Error(w, "unable to render home page", http.StatusInternalServerError)
		return
	}

	// If the method is GET, then we'll render the home page with the form
	// itself.
	switch {
	case r.Method == http.MethodGet:
		homeTemplate.Execute(w, homeInfo)

		// If the method isn't either of those, then this is an error as we
	// only support the two methods above.
	default:
		http.Error(w, "Method not allowed!", http.StatusMethodNotAllowed)
	}
}

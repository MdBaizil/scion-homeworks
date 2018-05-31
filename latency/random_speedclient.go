// Dedicated client for measuring speed (RTT and Latency)

package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/scionproto/scion/go/lib/common"
	"github.com/scionproto/scion/go/lib/hpkt"
	"github.com/scionproto/scion/go/lib/scmp"
	"github.com/scionproto/scion/go/lib/snet"
	"github.com/scionproto/scion/go/lib/spkt"
)

var Seed rand.Source

func createScmpEchoReqPkt(local *snet.Addr, remote *snet.Addr) (uint64, *spkt.ScnPkt) {
	id := rand.New(Seed).Uint64()
	info := &scmp.InfoEcho{Id: id, Seq: 0}

	scmpMeta := scmp.Meta{InfoLen: uint8(info.Len() / common.LineLen)}
	pld := make(common.RawBytes, scmp.MetaLen+info.Len())
	scmpMeta.Write(pld)
	info.Write(pld[scmp.MetaLen:])
	scmpHdr := scmp.NewHdr(scmp.ClassType{Class: scmp.C_General, Type: scmp.T_G_EchoRequest}, len(pld))

	pkt := &spkt.ScnPkt{
		DstIA:   remote.IA,
		SrcIA:   local.IA,
		DstHost: remote.Host,
		SrcHost: local.Host,
		Path:    remote.Path,
		HBHExt:  []common.Extension{},
		L4:      scmpHdr,
		Pld:     pld,
	}

	return id, pkt
}


func validatePkt(pkt *spkt.ScnPkt, id uint64) (*scmp.Hdr, *scmp.InfoEcho, error) {
	scmpHdr, ok := pkt.L4.(*scmp.Hdr)
	if !ok {
		return nil, nil,
			common.NewBasicError("Not an SCMP header", nil, "type", common.TypeOf(pkt.L4))
	}
	scmpPld, ok := pkt.Pld.(*scmp.Payload)
	if !ok {
		return nil, nil,
			common.NewBasicError("Not an SCMP payload", nil, "type", common.TypeOf(pkt.Pld))
	}
	info, ok := scmpPld.Info.(*scmp.InfoEcho)
	if !ok {
		return nil, nil,
			common.NewBasicError("Not an Info Echo", nil, "type", common.TypeOf(info))
	}
	return scmpHdr, info, nil
}

func check(e error) {
	if e != nil {
		log.Fatal(e)
	}
}

func printUsage() {
	fmt.Println("\nrandom_speedclient -s SourceSCIONAddress -d DestinationSCIONAddress")
	fmt.Println("\tProvides speed estimates (RTT and latency) from source to desination")
	fmt.Println("\tThe SCION address is specified as ISD-AS,[IP Address]:Port")
	fmt.Println("\tIf source port unspecified, a random available one will be used.")
	fmt.Println("\tExample SCION address 1-1,[127.0.0.1]:42002\n")
}

func main() {
	var (
		sourceAddress string
		destinationAddress string

		err    error
		local  *snet.Addr
		remote *snet.Addr

		scmpConnection *snet.Conn
	)

	// Fetch arguments from command line
	flag.StringVar(&sourceAddress, "s", "", "Source SCION Address")
	flag.StringVar(&destinationAddress, "d", "", "Destination SCION Address")
	flag.Parse()

	// Create the SCION UDP socket
	if len(sourceAddress) > 0 {
		local, err = snet.AddrFromString(sourceAddress)
		check(err)
	} else {
		printUsage()
		check(fmt.Errorf("Error, source address needs to be specified with -s"))
	}
	if len(destinationAddress) > 0 {
		remote, err = snet.AddrFromString(destinationAddress)
		check(err)
	} else {
		printUsage()
		check(fmt.Errorf("Error, destination address needs to be specified with -d"))
	}

	sciondAddr := fmt.Sprintf("/run/shm/sciond/sd%d-%d.sock", local.IA.I, local.IA.A)
	dispatcherAddr := "/run/shm/dispatcher/default.sock"
	snet.Init(local.IA, sciondAddr, dispatcherAddr)

	scmpConnection, err = snet.DialSCION("udp4", local, remote)
	check(err)

	receivePacketBuffer := make([]byte, 2500)

	Seed = rand.NewSource(time.Now().UnixNano())

	// Do 5 iterations so we can use average
	var total int64 = 0
	iters := 0
	num_tries := 0
	for iters < 5 && num_tries < 20 {
		num_tries += 1

		// Construct SCMP Packet
		id, pkt := createScmpEchoReqPkt(local, remote)
		b := make(common.RawBytes, common.MinMTU)
		pktLen, err := hpkt.WriteScnPkt(pkt, b)
		check(err)


		time_sent := time.Now()
		_, err = scmpConnection.Write(b[:pktLen])
		check(err)

		n, _, err := scmpConnection.ReadFrom(receivePacketBuffer)
		time_received := time.Now()

		recvpkt := &spkt.ScnPkt{}
		err = hpkt.ParseScnPkt(recvpkt, b[:n])
		check(err)
		_, info, err := validatePkt(recvpkt, id)
		check(err)

		if info.Id == id {
			total += (time_received.UnixNano() - time_sent.UnixNano())
			iters += 1
		}
	}

	if iters != 5 {
		check(fmt.Errorf("Error, exceeded maximum number of attempts"))
	}

	var difference float64 = float64(total) / float64(iters)

	fmt.Printf("Source: %s\nDestination: %s\n", sourceAddress, destinationAddress);
	fmt.Println("Time estimates:")
	// Print in ms, so divide by 1e6 from nano
	fmt.Printf("\tRTT - %.3fms\n", difference/1e6)
	fmt.Printf("\tLatency - %.3fms\n", difference/2e6)
}

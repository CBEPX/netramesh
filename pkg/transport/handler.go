package transport

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/patrickmn/go-cache"

	"github.com/Lookyan/netramesh/pkg/estabcache"
	"github.com/Lookyan/netramesh/pkg/log"
	"github.com/Lookyan/netramesh/pkg/protocol"
)

const SO_ORIGINAL_DST = 80

func tcpCopy(
	logger *log.Logger,
	r io.ReadWriteCloser,
	w io.ReadWriteCloser,
	initiator bool,
	netRequest protocol.NetRequest,
	netHandler protocol.NetHandler,
	isInBoundConn bool,
	done chan struct{}) {
	startD := time.Now()
	if initiator {
		netHandler.HandleRequest(r, w, netRequest, isInBoundConn)
	} else {
		netHandler.HandleResponse(r, w, netRequest, isInBoundConn)
	}

	logger.Debugf("TCP connection Duration: %s (initiator: %t)", time.Since(startD).String(), initiator)
	done <- struct{}{}
}

func HandleConnection(
	logger *log.Logger,
	conn *net.TCPConn,
	ec *estabcache.EstablishedCache,
	tracingContextMapping *cache.Cache) {
	if conn == nil {
		return
	}
	defer func() {
		logger.Debug("Closing src conn")
		// Important to close read operations
		// to avoid waiting for never ending read operation when client doesn't close connection
		conn.CloseRead()
		conn.CloseWrite()
		conn.Close()
		logger.Debug("Closed src conn")
	}()
	conn.SetNoDelay(true)

	f, err := conn.File()
	if err != nil {
		logger.Debug(err.Error())
		return
	}
	defer f.Close()
	err = syscall.SetNonblock(int(f.Fd()), true)
	if err != nil {
		logger.Debug("Can't turn fd into non-blocking mode")
	}

	addr, err := syscall.GetsockoptIPv6Mreq(int(f.Fd()), syscall.IPPROTO_IP, SO_ORIGINAL_DST)
	if err != nil {
		logger.Warning(err.Error())
		return
	}
	ipv4 := strconv.Itoa(int(addr.Multiaddr[4])) + "." +
		strconv.Itoa(int(addr.Multiaddr[5])) + "." +
		strconv.Itoa(int(addr.Multiaddr[6])) + "." +
		strconv.Itoa(int(addr.Multiaddr[7]))
	port := uint16(addr.Multiaddr[2])<<8 + uint16(addr.Multiaddr[3])

	isInBoundConn := ipv4 == strings.Split(conn.LocalAddr().String(), ":")[0]

	dstAddr := fmt.Sprintf("%s:%d", ipv4, port)
	logger.Debugf("From: %s To: %s", conn.RemoteAddr(), conn.LocalAddr())
	logger.Debugf("Original destination :: %s", dstAddr)

	tcpDstAddr, err := net.ResolveTCPAddr("tcp", dstAddr)
	if err != nil {
		logger.Warningf("Error while resolving tcp addr %s", dstAddr)
	}
	targetConn, err := net.DialTCP("tcp", nil, tcpDstAddr)
	if err != nil {
		logger.Warning(err.Error())
		return
	}
	defer func() {
		logger.Debug("Closing target conn")
		// same logic as for source tcp connection
		targetConn.CloseRead()
		targetConn.CloseWrite()
		targetConn.Close()
		logger.Debug("Closed target conn")
	}()

	// determine protocol and choose logic
	p := protocol.Determine(dstAddr)
	logger.Debugf("Determined %s protocol", p)
	netRequest := protocol.GetNetRequest(p, logger)
	netHandler := protocol.GetNetworkHandler(p, logger, tracingContextMapping)

	ec.Add(dstAddr)

	done := make(chan struct{}, 1)
	go tcpCopy(logger, conn, targetConn, true, netRequest, netHandler, isInBoundConn, done)
	go tcpCopy(logger, targetConn, conn, false, netRequest, netHandler, isInBoundConn, done)
	<-done

	logger.Debug("Finished")
	ec.Remove(dstAddr)
}

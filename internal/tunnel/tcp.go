package tunnel

import (
	"crypto/tls"
	"io"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/yosebyte/passport/internal"
	"github.com/yosebyte/passport/pkg/conn"
	"github.com/yosebyte/passport/pkg/log"
)

func ServeTCP(parsedURL *url.URL, whiteList *sync.Map, targetTCPListen *net.TCPListener, linkListen net.Listener, linkTLS *tls.Conn, mu *sync.Mutex, done <-chan struct{}) error {
	sem := make(chan struct{}, internal.MaxSemaphoreLimit)
	for {
		select {
		case <-done:
			return nil
		default:
			targetConn, err := targetTCPListen.AcceptTCP()
			if err != nil {
				log.Error("Unable to accept connections form target address: [%v] %v", targetTCPListen.Addr().String(), err)
				time.Sleep(1 * time.Second)
				continue
			}
			clientAddr := targetConn.RemoteAddr().String()
			log.Info("Target connection established from: [%v]", clientAddr)
			if parsedURL.Fragment != "" {
				clientIP, _, err := net.SplitHostPort(clientAddr)
				if err != nil {
					log.Error("Unable to extract client IP address: [%v] %v", clientAddr, err)
					targetConn.Close()
					time.Sleep(1 * time.Second)
					continue
				}
				if _, exists := whiteList.Load(clientIP); !exists {
					log.Warn("Unauthorized IP address blocked: [%v]", clientIP)
					targetConn.Close()
					continue
				}
			}
			sem <- struct{}{}
			go func(targetConn *net.TCPConn) {
				defer func() { <-sem }()
				mu.Lock()
				_, err = linkTLS.Write([]byte("[PASSPORT]<TCP>\n"))
				mu.Unlock()
				if err != nil {
					log.Error("Unable to send signal: %v", err)
					targetConn.Close()
					return
				}
				remoteConn, err := linkListen.Accept()
				if err != nil {
					log.Error("Unable to accept connections form link address: [%v] %v", linkListen.Addr().String(), err)
					return
				}
				remoteTLS, ok := remoteConn.(*tls.Conn)
				if !ok {
					log.Error("Non-TLS connection received")
					targetConn.Close()
					remoteConn.Close()
					return
				}
				if err := remoteTLS.Handshake(); err != nil {
					log.Error("TLS handshake failed: %v", err)
					targetConn.Close()
					remoteTLS.Close()
					return
				}
				log.Info("Starting data exchange: [%v] <-> [%v]", clientAddr, targetTCPListen.Addr().String())
				if err := conn.DataExchange(remoteTLS, targetConn); err != nil {
					if err == io.EOF {
						log.Info("Connection closed successfully: %v", err)
					} else {
						log.Warn("Connection closed unexpectedly: %v", err)
					}
				}
			}(targetConn)
		}
	}
}

func ClientTCP(linkAddr, targetTCPAddr *net.TCPAddr) {
	targetConn, err := net.DialTCP("tcp", nil, targetTCPAddr)
	if err != nil {
		log.Error("Unable to dial target address: [%v], %v", targetTCPAddr, err)
		return
	}
	log.Info("Target connection established: [%v]", targetTCPAddr)
	remoteTLS, err := tls.Dial("tcp", linkAddr.String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		log.Error("Unable to dial target address: [%v], %v", linkAddr, err)
		targetConn.Close()
		return
	}
	if err := remoteTLS.Handshake(); err != nil {
		log.Error("TLS handshake failed: %v", err)
		targetConn.Close()
		remoteTLS.Close()
		return
	}
	log.Info("Starting data exchange: [%v] <-> [%v]", linkAddr, targetTCPAddr)
	if err := conn.DataExchange(remoteTLS, targetConn); err != nil {
		if err == io.EOF {
			log.Info("Connection closed successfully: %v", err)
		} else {
			log.Warn("Connection closed unexpectedly: %v", err)
		}
	}
}

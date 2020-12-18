/*
 * tx_tcp.go: Tx Packet Processor
 *
 * Author: Davi Gupta (davigupta@gmail.com), Sep 2020
 */

package router

import (
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"minion.io/aaa"
	"minion.io/common"
	"minion.io/stats"
)

// TODO: Implement this to send a flow termination message back on the channel,
// so that the other end can take necessary action to terminate the flow. Also,
// this needs to be done only for "proxied" flows - ie TCP terminated flows. We
// can also be carrying raw IP packets here, that does not need  flow terminate.
// The parameter to this API is the list of http headers (nextensio headers and
// other general http headers) - the nextensio headers in there should be able to
// give us flow identification and information on whether the flow is raw IP or
// proxied etc..
func sendHttpFlowTerm(s *zap.SugaredLogger, headers map[string][]string, reason string, data []byte) {
	stats.PakDrop(data, "AccessDenied", s)
}

func sendLeft(w http.ResponseWriter, r *http.Request, s *zap.SugaredLogger) {
	defer r.Body.Close()
	dest := r.Header.Get("x-nextensio-for")
	destinfo := strings.Split(dest, ":")
	host := destinfo[0]
	host = strings.ReplaceAll(host, ".", "-")
	data := common.HttpToBytes(s, r)
	left := LookupLeftService(host)
	if left != nil {
		usr := r.Header.Get("x-nextensio-attr")
		if aaa.AccessOk(left.clitype, left.uuid, usr, s) == false {
			sendHttpFlowTerm(s, r.Header, "AccessFail", data)
			return
		}
		err := left.conn.WriteMessage(websocket.BinaryMessage, data)
		if err != nil {
			sendHttpFlowTerm(s, r.Header, "WriteFailedLeft", data)
			return
		}
	} else {
		sendHttpFlowTerm(s, r.Header, "LookupFailedLeft", data)
		return
	}
}

func sendRight(s *zap.SugaredLogger, lname string, rname string, r *http.Request) error {
	httpName := lname + ":" + rname
	client := getHttp(httpName)
	if client == nil {
		return errors.New("Cannot get http connection")
	}
	portStr := strconv.Itoa(common.MyInfo.Iport)
	dest := rname + ":" + portStr
	// TODO: The HTTP standard says that there is no body expected with a GET request.
	// As of today it works fine, but we dont know if at some point envoy will say just
	// stat ignoring the body of GET, better to move this to a PUT/POST
	req, err := http.NewRequest(http.MethodGet, "http://"+dest, r.Body)
	if err != nil {
		return err
	}
	for name, values := range r.Header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	clenStr := req.Header.Get("Content-Length")
	clen, err := strconv.ParseInt(clenStr, 10, 64)
	if err != nil {
		return err
	}
	req.ContentLength = clen
	resp, err := client.Do(req)
	if err != nil {
		// Some error in the transport, like maybe the destination pod crashed ?
		// Discard this transport and open a new one
		delHttp(httpName)
		return err
	}
	// The response has to be read AND body has to be closed so that the underlying
	// client transport can be reused for another http request
	io.Copy(ioutil.Discard, resp.Body)
	resp.Body.Close()

	return nil
}

// Start http server to deal with http requests from inside this cluster
func HttpRightStart(s *zap.SugaredLogger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sendLeft(w, r, s)
	})
	portStr := ":" + strconv.Itoa(common.MyInfo.Iport)
	server := http.Server{Addr: portStr, Handler: mux}
	e := server.ListenAndServe()
	if e != nil {
		s.Errorw("http server start failure", "err", e)
	}
}

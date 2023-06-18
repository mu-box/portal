package api

import (
	"net/http"

	"github.com/mu-box/portal/cluster"
	"github.com/mu-box/portal/core"
	"github.com/mu-box/portal/core/common"
)

func postVip(rw http.ResponseWriter, req *http.Request) {
	var vip core.Vip
	err := parseBody(req, &vip)
	if err != nil {
		writeError(rw, req, err, http.StatusBadRequest)
		return
	}

	// save to cluster
	err = cluster.SetVip(vip)
	if err != nil {
		writeError(rw, req, err, http.StatusInternalServerError)
		return
	}

	writeBody(rw, req, vip, http.StatusOK)
}

func deleteVip(rw http.ResponseWriter, req *http.Request) {
	var vip core.Vip
	err := parseBody(req, &vip)
	if err != nil {
		writeError(rw, req, err, http.StatusBadRequest)
		return
	}

	// save to cluster
	err = cluster.DeleteVip(vip)
	if err != nil {
		writeError(rw, req, err, http.StatusInternalServerError)
		return
	}

	writeBody(rw, req, apiMsg{"Success"}, http.StatusOK)
}

func putVips(rw http.ResponseWriter, req *http.Request) {
	var vips []core.Vip
	err := parseBody(req, &vips)
	if err != nil {
		writeError(rw, req, err, http.StatusBadRequest)
		return
	}

	// save to cluster
	err = cluster.SetVips(vips)
	if err != nil {
		writeError(rw, req, err, http.StatusInternalServerError)
		return
	}

	writeBody(rw, req, vips, http.StatusOK)
}

// List the vips registered in my system
func getVips(rw http.ResponseWriter, req *http.Request) {
	vips, err := common.GetVips()
	if err != nil {
		writeError(rw, req, err, http.StatusInternalServerError)
		return
	}
	writeBody(rw, req, vips, http.StatusOK)
}

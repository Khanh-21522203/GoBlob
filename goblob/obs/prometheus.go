package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func prometheusHandler() http.Handler {
	return promhttp.Handler()
}

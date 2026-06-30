package api

import (
	"aurora/internal/bootstrap"
	"github.com/gin-gonic/gin"
	"net/http"
)

var router *gin.Engine

func init() {
	app, err := bootstrap.Init()
	if err != nil {
		panic(err)
	}
	router = app.Router
}

func Listen(w http.ResponseWriter, r *http.Request) {
	router.ServeHTTP(w, r)
}

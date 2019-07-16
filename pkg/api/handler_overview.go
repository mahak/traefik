package api

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/containous/traefik/pkg/config/runtime"
	"github.com/containous/traefik/pkg/config/static"
	"github.com/containous/traefik/pkg/log"
)

type schemeOverview struct {
	Routers     *section `json:"routers,omitempty"`
	Services    *section `json:"services,omitempty"`
	Middlewares *section `json:"middlewares,omitempty"`
}

type section struct {
	Total    int `json:"total"`
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
}

type features struct {
	Tracing   string `json:"tracing"`
	Metrics   string `json:"metrics"`
	AccessLog bool   `json:"accessLog"`
	// TODO add certificates resolvers
}

type overview struct {
	HTTP      schemeOverview `json:"http"`
	TCP       schemeOverview `json:"tcp"`
	Features  features       `json:"features,omitempty"`
	Providers []string       `json:"providers,omitempty"`
}

func (h Handler) getOverview(rw http.ResponseWriter, request *http.Request) {
	result := overview{
		HTTP: schemeOverview{
			Routers:     getHTTPRouterSection(h.runtimeConfiguration.Routers),
			Services:    getHTTPServiceSection(h.runtimeConfiguration.Services),
			Middlewares: getHTTPMiddlewareSection(h.runtimeConfiguration.Middlewares),
		},
		TCP: schemeOverview{
			Routers:  getTCPRouterSection(h.runtimeConfiguration.TCPRouters),
			Services: getTCPServiceSection(h.runtimeConfiguration.TCPServices),
		},
		Features:  getFeatures(h.staticConfig),
		Providers: getProviders(h.staticConfig),
	}

	rw.Header().Set("Content-Type", "application/json")

	err := json.NewEncoder(rw).Encode(result)
	if err != nil {
		log.FromContext(request.Context()).Error(err)
		http.Error(rw, err.Error(), http.StatusInternalServerError)
	}
}

func getHTTPRouterSection(routers map[string]*runtime.RouterInfo) *section {
	var countErrors int
	var countWarnings int
	for _, rt := range routers {
		switch rt.Status {
		case runtime.StatusDisabled:
			countErrors++
		case runtime.StatusWarning:
			countWarnings++
		}
	}

	return &section{
		Total:    len(routers),
		Warnings: countWarnings,
		Errors:   countErrors,
	}
}

func getHTTPServiceSection(services map[string]*runtime.ServiceInfo) *section {
	var countErrors int
	var countWarnings int
	for _, svc := range services {
		switch svc.Status {
		case runtime.StatusDisabled:
			countErrors++
		case runtime.StatusWarning:
			countWarnings++
		}
	}

	return &section{
		Total:    len(services),
		Warnings: countWarnings,
		Errors:   countErrors,
	}
}

func getHTTPMiddlewareSection(middlewares map[string]*runtime.MiddlewareInfo) *section {
	var countErrors int
	for _, md := range middlewares {
		if md.Err != nil {
			countErrors++
		}
	}

	return &section{
		Total:    len(middlewares),
		Warnings: 0,
		Errors:   countErrors,
	}
}

func getTCPRouterSection(routers map[string]*runtime.TCPRouterInfo) *section {
	var countErrors int
	for _, rt := range routers {
		if rt.Err != "" {
			countErrors++
		}
	}

	return &section{
		Total:    len(routers),
		Warnings: 0, // TODO
		Errors:   countErrors,
	}
}

func getTCPServiceSection(services map[string]*runtime.TCPServiceInfo) *section {
	var countErrors int
	for _, svc := range services {
		if svc.Err != nil {
			countErrors++
		}
	}

	return &section{
		Total:    len(services),
		Warnings: 0, // TODO
		Errors:   countErrors,
	}
}

func getProviders(conf static.Configuration) []string {
	if conf.Providers == nil {
		return nil
	}

	var providers []string

	v := reflect.ValueOf(conf.Providers).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() == reflect.Ptr && field.Elem().Kind() == reflect.Struct {
			if !field.IsNil() {
				providers = append(providers, v.Type().Field(i).Name)
			}
		}
	}

	return providers
}

func getFeatures(conf static.Configuration) features {
	return features{
		Tracing:   getTracing(conf),
		Metrics:   getMetrics(conf),
		AccessLog: conf.AccessLog != nil,
	}
}

func getMetrics(conf static.Configuration) string {
	if conf.Metrics == nil {
		return ""
	}

	v := reflect.ValueOf(conf.Metrics).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() == reflect.Ptr && field.Elem().Kind() == reflect.Struct {
			if !field.IsNil() {
				return v.Type().Field(i).Name
			}
		}
	}

	return ""
}

func getTracing(conf static.Configuration) string {
	if conf.Tracing == nil {
		return ""
	}

	v := reflect.ValueOf(conf.Tracing).Elem()
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		if field.Kind() == reflect.Ptr && field.Elem().Kind() == reflect.Struct {
			if !field.IsNil() {
				return v.Type().Field(i).Name
			}
		}
	}

	return ""
}
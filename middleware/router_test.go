// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	stdcontext "context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-openapi/analysis"
	"github.com/go-openapi/loads"
	"github.com/go-openapi/runtime/internal/testing/petstore"
	"github.com/go-openapi/runtime/middleware/untyped"
	"github.com/go-openapi/testify/v2/assert"
	"github.com/go-openapi/testify/v2/require"
)

func terminator(rw http.ResponseWriter, _ *http.Request) {
	rw.WriteHeader(http.StatusOK)
}

func TestRouterMiddleware(t *testing.T) {
	spec, api := petstore.NewAPI(t)
	context := NewContext(spec, api, nil)
	mw := NewRouter(context, http.HandlerFunc(terminator))

	recorder := httptest.NewRecorder()
	request, err := http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/api/pets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)

	recorder = httptest.NewRecorder()
	request, err = http.NewRequestWithContext(stdcontext.Background(), http.MethodDelete, "/api/pets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)

	methods := strings.Split(recorder.Header().Get("Allow"), ",")
	sort.Strings(methods)
	assert.Equal(t, "GET,POST", strings.Join(methods, ","))

	recorder = httptest.NewRecorder()
	request, err = http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/nopets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.Equal(t, http.StatusNotFound, recorder.Code)

	recorder = httptest.NewRecorder()
	request, err = http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/pets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusNotFound, recorder.Code)

	spec, api = petstore.NewRootAPI(t)
	context = NewContext(spec, api, nil)
	mw = NewRouter(context, http.HandlerFunc(terminator))

	recorder = httptest.NewRecorder()
	request, err = http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/pets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)

	recorder = httptest.NewRecorder()
	request, err = http.NewRequestWithContext(stdcontext.Background(), http.MethodDelete, "/pets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)

	methods = strings.Split(recorder.Header().Get("Allow"), ",")
	sort.Strings(methods)
	assert.Equal(t, "GET,POST", strings.Join(methods, ","))

	recorder = httptest.NewRecorder()
	request, err = http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/nopets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestRouterBuilder(t *testing.T) {
	spec, api := petstore.NewAPI(t)
	analyzed := analysis.New(spec.Spec())

	assert.Len(t, analyzed.RequiredConsumes(), 3)
	assert.Len(t, analyzed.RequiredProduces(), 5)
	assert.Len(t, analyzed.OperationIDs(), 4)

	// context := NewContext(spec, api)
	builder := petAPIRouterBuilder(spec, api, analyzed)
	getRecords := builder.records[http.MethodGet]
	postRecords := builder.records[http.MethodPost]
	deleteRecords := builder.records[http.MethodDelete]

	assert.Len(t, getRecords, 2)
	assert.Len(t, postRecords, 1)
	assert.Len(t, deleteRecords, 1)

	assert.Empty(t, builder.records[http.MethodPatch])
	assert.Empty(t, builder.records[http.MethodOptions])
	assert.Empty(t, builder.records[http.MethodHead])
	assert.Empty(t, builder.records[http.MethodPut])

	rec := postRecords[0]
	assert.Equal(t, "/pets", rec.Key)
	val := rec.Value.(*routeEntry)
	assert.Len(t, val.Consumers, 2)
	assert.Len(t, val.Producers, 2)
	assert.Len(t, val.Consumes, 2)
	assert.Len(t, val.Produces, 2)

	assert.Contains(t, val.Consumers, "application/json")
	assert.Contains(t, val.Producers, "application/x-yaml")
	assert.Contains(t, val.Consumes, "application/json")
	assert.Contains(t, val.Produces, "application/x-yaml")

	assert.Len(t, val.Parameters, 1)

	recG := getRecords[0]
	assert.Equal(t, "/pets", recG.Key)
	valG := recG.Value.(*routeEntry)
	assert.Len(t, valG.Consumers, 2)
	assert.Len(t, valG.Producers, 4)
	assert.Len(t, valG.Consumes, 2)
	assert.Len(t, valG.Produces, 4)

	assert.Len(t, valG.Parameters, 2)
}

func TestRouterCanonicalBasePath(t *testing.T) {
	spec, api := petstore.NewAPI(t)
	spec.Spec().BasePath = "/api///"
	context := NewContext(spec, api, nil)
	mw := NewRouter(context, http.HandlerFunc(terminator))

	recorder := httptest.NewRecorder()
	request, err := http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/api/pets", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestRouter_EscapedPath(t *testing.T) {
	spec, api := petstore.NewAPI(t)
	spec.Spec().BasePath = "/api/"
	context := NewContext(spec, api, nil)
	mw := NewRouter(context, http.HandlerFunc(terminator))

	recorder := httptest.NewRecorder()
	request, err := http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/api/pets/123", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)

	recorder = httptest.NewRecorder()
	request, err = http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/api/pets/abc%2Fdef", nil)
	require.NoError(t, err)

	mw.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)
	ri, _, _ := context.RouteInfo(request)
	require.NotNil(t, ri)
	require.NotNil(t, ri.Params)
	assert.Equal(t, "abc/def", ri.Params.Get("id"))
}

func TestRouterStruct(t *testing.T) {
	spec, api := petstore.NewAPI(t)
	router := DefaultRouter(spec, newRoutableUntypedAPI(spec, api, new(Context)))

	methods := router.OtherMethods("post", "/api/pets/{id}")
	assert.Len(t, methods, 2)

	entry, ok := router.Lookup("delete", "/api/pets/{id}")
	assert.True(t, ok)
	require.NotNil(t, entry)
	assert.Len(t, entry.Params, 1)
	assert.Equal(t, "id", entry.Params[0].Name)

	_, ok = router.Lookup("delete", "/pets")
	assert.False(t, ok)

	_, ok = router.Lookup("post", "/no-pets")
	assert.False(t, ok)
}

func petAPIRouterBuilder(spec *loads.Document, api *untyped.API, analyzed *analysis.Spec) *defaultRouteBuilder {
	builder := newDefaultRouteBuilder(spec, newRoutableUntypedAPI(spec, api, new(Context)))
	builder.AddRoute(http.MethodGet, "/pets", analyzed.AllPaths()["/pets"].Get)
	builder.AddRoute(http.MethodPost, "/pets", analyzed.AllPaths()["/pets"].Post)
	builder.AddRoute(http.MethodDelete, "/pets/{id}", analyzed.AllPaths()["/pets/{id}"].Delete)
	builder.AddRoute(http.MethodGet, "/pets/{id}", analyzed.AllPaths()["/pets/{id}"].Get)

	return builder
}

func TestPathConverter(t *testing.T) {
	cases := []struct {
		swagger string
		denco   string
	}{
		{"/", "/"},
		{"/something", "/something"},
		{"/{id}", "/:id"},
		{"/{id}/something/{anotherId}", "/:id/something/:anotherId"},
		{"/{petid}", "/:petid"},
		{"/{pet_id}", "/:pet_id"},
		{"/{petId}", "/:petId"},
		{"/{pet-id}", "/:pet-id"},
		// compost parameters tests
		{"/p_{pet_id}", "/p_:pet_id"},
		{"/p_{petId}.{petSubId}", "/p_:petId"},
	}

	for _, tc := range cases {
		actual := pathConverter.ReplaceAllString(tc.swagger, ":$1")
		assert.Equal(t, tc.denco, actual, "expected swagger path %s to match %s but got %s", tc.swagger, tc.denco, actual)
	}
}

func TestExtractCompositParameters(t *testing.T) {
	// name is the composite parameter's name, value is the value of this compost parameter, pattern is the pattern to be matched
	cases := []struct {
		name    string
		value   string
		pattern string
		names   []string
		values  []string
	}{
		{name: "fragment", value: "gie", pattern: "e", names: []string{"fragment"}, values: []string{"gi"}},
		{name: "fragment", value: "t.simpson", pattern: ".{subfragment}", names: []string{"fragment", "subfragment"}, values: []string{"t", "simpson"}},
	}
	for _, tc := range cases {
		names, values := decodeCompositParams(tc.name, tc.value, tc.pattern, nil, nil)
		assert.Equal(t, tc.names, names)
		assert.Equal(t, tc.values, values)
	}
}

func TestRouterContext_Issue375(t *testing.T) {
	// asserts request context propagation in a middleware stack:
	//
	// startStack -> Router -> customHandler -> endStack
	spec, api := petstore.NewAPI(t)
	spec.Spec().BasePath = "/api/"
	apiContext := NewContext(spec, api, nil)

	type ctxKey uint8

	const (
		beforeCtx ctxKey = iota + 1
		afterCtx
	)

	beforeCtxErr := stderrors.New("test error inserted in context before routing")
	afterCtxErr := stderrors.New("test error inserted in context after routing")

	// endStack is invoked after the custom handler
	endStack := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("called endStack")

		beforeValue := r.Context().Value(beforeCtx)
		assert.NotNilf(t, beforeValue, "end of middleware chain: expected to find beforeCtx in request context")
		if beforeValue == nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		afterValue := r.Context().Value(afterCtx)
		assert.NotNilf(t, afterValue, "end of middleware chain: expected to find afterCtx in request context")
		if afterValue == nil {
			w.WriteHeader(http.StatusInternalServerError)
		}

		fmt.Fprintf(w, `{beforeCtx="%v",afterCtx="%v"}`, beforeValue, afterValue)
	})

	// custom handler asserts that the initial context has been conveyed and adds some new context value
	customHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// check context after API middleware
		beforeValue := r.Context().Value(beforeCtx)
		assert.NotNilf(t, beforeValue, "after routing: expected to find beforeCtx in request context")
		if beforeValue == nil {
			w.WriteHeader(http.StatusInternalServerError)
		}

		errAuth, ok := beforeValue.(error)
		assert.Truef(t, ok, "expected beforeCtx to be an error, but got: %T", beforeValue)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
		}

		t.Logf(`called customHandler with beforeCtx from request: "%v"`, errAuth)

		// insert new context after API middleware
		afterContext := stdcontext.WithValue(r.Context(), afterCtx, afterCtxErr)
		*r = *r.WithContext(afterContext)

		//endStack.ServeHTTP(w, r.WithContext(afterContext))
		endStack.ServeHTTP(w, r)
	})

	// router invokes customHandler for this API context
	router := NewRouter(apiContext, customHandler)

	// startStack sets some context then invokes router
	startStack := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beforeContext := stdcontext.WithValue(r.Context(), beforeCtx, beforeCtxErr)
		t.Logf(`calling router with initial context: "%v"`, beforeCtxErr)

		router.ServeHTTP(w, r.WithContext(beforeContext))
	})

	recorder := httptest.NewRecorder()
	request, err := http.NewRequestWithContext(stdcontext.Background(), http.MethodGet, "/api/pets/123", nil)
	require.NoError(t, err)

	startStack.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusOK, recorder.Code)

	beforeValue := request.Context().Value(beforeCtx)
	t.Logf("before value in request: %v", beforeValue)
	afterValue := request.Context().Value(afterCtx)
	t.Logf("after value in request: %v", afterValue)

	res := recorder.Result()
	require.NotNil(t, res.Body)
	msg, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	t.Logf("response message: %q", string(msg))
}

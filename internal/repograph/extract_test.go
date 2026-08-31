package repograph

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractFileGo(t *testing.T) {
	t.Parallel()

	data := []byte(`package api

import (
	"fmt"
	"io"
	"os"
)

type Service struct {
	io.Reader
}

func (s *Service) Serve() {
	_ = os.Getenv("API_TOKEN")
	router.GET("/users/:id", s.handle)
	http.HandleFunc("POST /orders/{id}", s.handle)
	fmt.Println("request complete")
}
`)
	facts := extractFile("/repo/api.go", "api.go", data, "digest")

	require.Equal(t, "go", facts.Language)
	require.Equal(t, int64(len(data)), facts.Size)
	require.Contains(t, facts.Symbols, SymbolFact{
		Name: "Serve", Qualified: "Service.Serve", Kind: "method",
		Signature: "func (s *Service) Serve()", StartLine: 13, EndLine: 18,
		Parent: "Service", Exported: true,
	})
	require.Contains(t, facts.Imports, ImportFact{Target: "io", Line: 5})
	require.Contains(t, facts.Calls, CallFact{Caller: "Service.Serve", Callee: "router.GET", Line: 15})
	require.Contains(t, facts.Literals, LiteralFact{Value: "API_TOKEN", Kind: "env", Line: 14})
	require.Contains(t, facts.Routes, RouteFact{Method: "GET", Path: "/users/{*}", Owner: "Service.Serve", Line: 15})
	require.Contains(t, facts.Routes, RouteFact{Method: "POST", Path: "/orders/{*}", Owner: "Service.Serve", Line: 16})
	require.Contains(t, facts.Inheritance, InheritanceFact{Child: "Service", Parent: "io.Reader", Line: 10})
}

func TestExtractFileTypeScript(t *testing.T) {
	t.Parallel()

	data := []byte(`import { Router } from "express";

export class Users extends Controller implements Resource {
  list() {
    const token = process.env.API_TOKEN;
    router.get("/users/:id", fetchUser);
    return fetchUser(token);
  }
}
`)
	facts := extractFile("src/users.ts", "src/users.ts", data, "digest")

	require.Contains(t, facts.Symbols, SymbolFact{
		Name: "Users", Qualified: "Users", Kind: "class",
		Signature: "export class Users extends Controller implements Resource {", StartLine: 3, EndLine: 9,
		Exported: true,
	})
	require.Contains(t, facts.Symbols, SymbolFact{
		Name: "list", Qualified: "Users.list", Kind: "method", Signature: "list() {",
		StartLine: 4, EndLine: 8, Parent: "Users", Exported: true,
	})
	require.Contains(t, facts.Imports, ImportFact{Target: "express", Line: 1})
	require.Contains(t, facts.Calls, CallFact{Caller: "Users.list", Callee: "fetchUser", Line: 7})
	require.Contains(t, facts.Literals, LiteralFact{Value: "API_TOKEN", Kind: "env", Line: 5})
	require.Contains(t, facts.Routes, RouteFact{Method: "GET", Path: "/users/{*}", Owner: "Users.list", Line: 6})
	require.Contains(t, facts.Inheritance, InheritanceFact{Child: "Users", Parent: "Controller", Line: 3})
	require.Contains(t, facts.Inheritance, InheritanceFact{Child: "Users", Parent: "Resource", Line: 3})
}

func TestExtractDjangoRelativeRoute(t *testing.T) {
	t.Parallel()

	data := []byte("urlpatterns = [path(\"orders/<int:id>/\", get_order)]\n")
	facts := extractFile("urls.py", "urls.py", data, "digest")
	require.Contains(t, facts.Routes, RouteFact{Path: "/orders/{*}", Line: 1})
}

func TestExtractConfigRoutesRejectsFilesystemPathDecoys(t *testing.T) {
	t.Parallel()

	data := []byte("cache_path: \"/tmp/cache\"\npublic_route: \"/orders/{id}\"\n")
	facts := extractFile("service.yaml", "service.yaml", data, "digest")
	require.Contains(t, facts.Routes, RouteFact{Path: "/orders/{*}", Line: 2})
	for _, route := range facts.Routes {
		require.NotEqual(t, "/tmp/cache", route.Path)
	}
}

func TestExtractFileComposesControllerRoutePrefixes(t *testing.T) {
	t.Parallel()

	typescript := []byte(`@Controller("users")
export class UsersController {
  @Get(":id")
  getUser() {}
}
`)
	facts := extractFile("users.ts", "users.ts", typescript, "digest")
	require.Contains(t, facts.Routes, RouteFact{
		Method: "GET", Path: "/users/{*}", Owner: "UsersController.getUser", Line: 3,
	})

	java := []byte(`@RequestMapping("orders")
public class OrdersController {
  @PostMapping("/{id}")
  public void update() {}
}
`)
	facts = extractFile("OrdersController.java", "OrdersController.java", java, "digest")
	require.Contains(t, facts.Routes, RouteFact{
		Method: "POST", Path: "/orders/{*}", Owner: "OrdersController.update", Line: 3,
	})
}

func TestExtractNestedRoutePrefixes(t *testing.T) {
	t.Parallel()

	ktor := []byte(`fun install() {
  route("/api") {
    route("/orders") {
      get("/{id}") {}
    }
  }
}
`)
	facts := extractFile("Routes.kt", "Routes.kt", ktor, "digest")
	require.Contains(t, facts.Routes, RouteFact{
		Method: "GET", Path: "/api/orders/{*}", Owner: "install", Line: 4,
	})

	phoenix := []byte(`defmodule Router do
  scope "api" do
    get "/users/:id", UserController, :show
  end
end
`)
	facts = extractFile("router.ex", "router.ex", phoenix, "digest")
	require.Contains(t, facts.Routes, RouteFact{
		Method: "GET", Path: "/api/users/{*}", Owner: "Router", Line: 3,
	})
}

func TestExtractFrameworkRouteMatrix(t *testing.T) {
	t.Parallel()

	goFacts := extractFile("routes.go", "routes.go", []byte(`package routes
func install() {
  router.MethodFunc("PATCH", "/orders/{id}", handler)
}
`), "digest")
	require.Contains(t, goFacts.Routes, RouteFact{
		Method: "PATCH", Path: "/orders/{*}", Owner: "routes.install", Line: 3,
	})

	typeScriptFacts := extractFile("client.ts", "client.ts", []byte(`async function load() {
  return fetch("/api/items/1")
}
`), "digest")
	require.Contains(t, typeScriptFacts.Routes, RouteFact{
		Method: "GET", Path: "/api/items/1", Owner: "load", Line: 2,
	})

	pythonFacts := extractFile("routes.py", "routes.py", []byte(`def install(app):
    app.add_url_rule("/health", health)
    web.route("PATCH", "/items/{id}", handler)
    app.router.add_get("/ready", ready)
    url(r"legacy/{id}", legacy)
`), "digest")
	require.Contains(t, pythonFacts.Routes, RouteFact{Path: "/health", Owner: "install", Line: 2})
	require.Contains(t, pythonFacts.Routes, RouteFact{Method: "PATCH", Path: "/items/{*}", Owner: "install", Line: 3})
	require.Contains(t, pythonFacts.Routes, RouteFact{Method: "GET", Path: "/ready", Owner: "install", Line: 4})
	require.Contains(t, pythonFacts.Routes, RouteFact{Path: "/legacy/{*}", Owner: "install", Line: 5})

	kotlinFacts := extractFile("Redirect.kt", "Redirect.kt", []byte(`fun redirect(call: Call) {
  call.respondRedirect("/login")
}
`), "digest")
	require.Contains(t, kotlinFacts.Routes, RouteFact{
		Method: "GET", Path: "/login", Owner: "redirect", Line: 2,
	})

	rubyFacts := extractFile("routes.rb", "routes.rb", []byte("def routes\n  redirect \"/new\"\n  root \"/home\"\nend\n"), "digest")
	require.Contains(t, rubyFacts.Routes, RouteFact{
		Method: "GET", Path: "/new", Owner: "routes", Line: 2,
	})
	require.Contains(t, rubyFacts.Routes, RouteFact{Path: "/home", Owner: "routes", Line: 3})

	elixirFacts := extractFile("router.ex", "router.ex", []byte("defmodule Router do\n  resources(\"/accounts\", AccountController)\nend\n"), "digest")
	require.Contains(t, elixirFacts.Routes, RouteFact{Path: "/accounts", Owner: "Router", Line: 2})
}

func TestExtractComponentLanguageSemantics(t *testing.T) {
	t.Parallel()

	facts := extractFile("src/routes/orders/+page.svelte", "src/routes/orders/+page.svelte", []byte(`<script lang="ts">
import { loadOrders } from "$lib/orders";
export class OrdersStore {
  refresh() { return loadOrders(); }
}
</script>
`), "digest")
	require.Equal(t, "svelte", facts.Language)
	require.Contains(t, facts.Imports, ImportFact{Target: "$lib/orders", Line: 2})
	require.True(t, slices.ContainsFunc(facts.Symbols, func(symbol SymbolFact) bool {
		return symbol.Name == "OrdersStore" && symbol.Kind == "class"
	}))
	require.True(t, slices.ContainsFunc(facts.Symbols, func(symbol SymbolFact) bool {
		return symbol.Name == "refresh" && symbol.Parent == "OrdersStore"
	}))
	require.Contains(t, facts.Calls, CallFact{Caller: "OrdersStore.refresh", Callee: "loadOrders", Line: 4})
	require.Contains(t, facts.Routes, RouteFact{Path: "/orders", Owner: "+page.svelte", Line: 1})
}

func TestExtractFileConventionRoutes(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"app/users/[id]/page.tsx":        "/users/{*}",
		"src/routes/admin/+page.svelte":  "/admin",
		"pages/orders/[id].vue":          "/orders/{*}",
		"src/pages/blog/[slug].astro":    "/blog/{*}",
		"app/(marketing)/about/page.tsx": "/about",
	}
	for filePath, expected := range cases {
		facts := extractFile(filePath, filePath, []byte("export default function Page() {}\n"), "digest")
		require.Contains(t, facts.Routes, RouteFact{Path: expected, Owner: filepath.Base(filePath), Line: 1})
	}

	nextRoute := "app/orders/[id]/route.ts"
	facts := extractFile(nextRoute, nextRoute, []byte("export async function GET() {}\nexport const POST = async () => {}\n"), "digest")
	require.Contains(t, facts.Routes, RouteFact{Method: "GET", Path: "/orders/{*}", Owner: "route.ts", Line: 1})
	require.Contains(t, facts.Routes, RouteFact{Method: "POST", Path: "/orders/{*}", Owner: "route.ts", Line: 1})

	svelteRoute := "src/routes/accounts/+server.ts"
	facts = extractFile(svelteRoute, svelteRoute, []byte("export function DELETE() {}\n"), "digest")
	require.Contains(t, facts.Routes, RouteFact{Method: "DELETE", Path: "/accounts", Owner: "+server.ts", Line: 1})

	nuxtRoute := "server/api/users/[id].patch.ts"
	facts = extractFile(nuxtRoute, nuxtRoute, []byte("export default defineEventHandler(() => {})\n"), "digest")
	require.Contains(t, facts.Routes, RouteFact{Method: "PATCH", Path: "/api/users/{*}", Owner: "[id].patch.ts", Line: 1})
}

func TestExtractOutlineLanguageTiers(t *testing.T) {
	t.Parallel()

	type expectedSymbol struct {
		name   string
		kind   string
		parent string
	}
	cases := []struct {
		name    string
		path    string
		source  string
		symbols []expectedSymbol
		imports []string
	}{
		{
			name: "c", path: "service.c",
			source:  "#include \"service.h\"\nstruct Service {\n  int value;\n};\nint process_order(int id) {\n  return id;\n}\n",
			symbols: []expectedSymbol{{name: "Service", kind: "struct"}, {name: "process_order", kind: "function"}},
			imports: []string{"service.h"},
		},
		{
			name: "cpp", path: "worker.cpp",
			source:  "class Worker {};\nint Worker::run() { return 1; }\n",
			symbols: []expectedSymbol{{name: "Worker", kind: "class"}, {name: "run", kind: "method", parent: "Worker"}},
		},
		{
			name: "elixir", path: "orders.ex",
			source:  "defmodule Orders do\n  alias App.Repo\n  def fetch(id) do\n    Repo.get(id)\n  end\nend\n",
			symbols: []expectedSymbol{{name: "Orders", kind: "module"}, {name: "fetch", kind: "method", parent: "Orders"}},
			imports: []string{"App.Repo"},
		},
		{
			name: "lua", path: "orders.lua",
			source:  "local http = require(\"http\")\nfunction Orders.fetch(id)\n  return id\nend\n",
			symbols: []expectedSymbol{{name: "fetch", kind: "method", parent: "Orders"}},
			imports: []string{"http"},
		},
		{
			name: "php", path: "Orders.php",
			source:  "<?php\nuse App\\Repository;\nclass Orders extends BaseOrder {\n  public function fetch($id) { return $id; }\n}\n",
			symbols: []expectedSymbol{{name: "Orders", kind: "class"}, {name: "fetch", kind: "method", parent: "Orders"}},
			imports: []string{"App\\Repository"},
		},
		{
			name: "swift", path: "OrderService.swift",
			source:  "import Foundation\nstruct OrderService: Sendable {\n  func fetch(id: Int) -> Int { id }\n}\n",
			symbols: []expectedSymbol{{name: "OrderService", kind: "struct"}, {name: "fetch", kind: "method", parent: "OrderService"}},
			imports: []string{"Foundation"},
		},
		{
			name: "scala", path: "OrderService.scala",
			source:  "import scala.concurrent.Future\nclass OrderService extends BaseService {\n  def fetch(id: Int) = { id }\n}\n",
			symbols: []expectedSymbol{{name: "OrderService", kind: "class"}, {name: "fetch", kind: "method", parent: "OrderService"}},
			imports: []string{"scala.concurrent.Future"},
		},
		{
			name: "haskell", path: "Order.hs",
			source:  "module Order where\nimport Data.Text\ndata Order = Order Int\nfetchOrder :: Order -> Int\nfetchOrder (Order value) = value\n",
			symbols: []expectedSymbol{{name: "Order", kind: "data"}, {name: "fetchOrder", kind: "function"}},
			imports: []string{"Data.Text"},
		},
		{
			name: "hcl", path: "main.tf",
			source:  "resource \"aws_instance\" \"web\" {\n  ami = \"ami-12345678\"\n}\n",
			symbols: []expectedSymbol{{name: "aws_instance.web", kind: "resource"}, {name: "ami", kind: "key", parent: "aws_instance.web"}},
		},
		{
			name: "env", path: ".env",
			source:  "DATABASE_URL=postgres://localhost/orders\nPORT=8080\n",
			symbols: []expectedSymbol{{name: "DATABASE_URL", kind: "key"}, {name: "PORT", kind: "key"}},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			facts := extractFile(testCase.path, testCase.path, []byte(testCase.source), "digest")
			for _, expected := range testCase.symbols {
				require.True(t, slices.ContainsFunc(facts.Symbols, func(symbol SymbolFact) bool {
					return symbol.Name == expected.name && symbol.Kind == expected.kind && symbol.Parent == expected.parent
				}), "%s %s", testCase.name, expected.name)
			}
			for _, expected := range testCase.imports {
				require.True(t, slices.ContainsFunc(facts.Imports, func(fact ImportFact) bool {
					return fact.Target == expected
				}), "%s %s", testCase.name, expected)
			}
			if testCase.name == "env" {
				require.Contains(t, facts.Literals, LiteralFact{Value: "DATABASE_URL", Kind: "env", Line: 1})
				require.Contains(t, facts.Literals, LiteralFact{Value: "PORT", Kind: "env", Line: 2})
			}
		})
	}
}

func TestExtractFileOutlinesAndGenerated(t *testing.T) {
	t.Parallel()

	markdown := []byte("# Guide\nintro\n## Install\nsteps\n# API\n")
	facts := extractFile("README.md", "README.md", markdown, "digest")
	require.Contains(t, facts.Symbols, SymbolFact{
		Name: "Guide", Qualified: "Guide", Kind: "heading", Signature: "# Guide",
		StartLine: 1, EndLine: 4,
	})
	require.Contains(t, facts.Symbols, SymbolFact{
		Name: "Install", Qualified: "Guide.Install", Kind: "heading", Signature: "## Install",
		StartLine: 3, EndLine: 4, Parent: "Guide",
	})

	generated := extractFile("client.js", "client.js", []byte("// Code generated by tool. DO NOT EDIT.\ncall();\n"), "digest")
	require.True(t, generated.Generated)
	require.Empty(t, generated.Symbols)
	require.Empty(t, generated.Calls)

	legitimate := extractFile(
		"messages.go",
		"messages.go",
		[]byte("package messages\n\nconst notice = \"generated by a command\"\n"),
		"digest",
	)
	require.False(t, legitimate.Generated)

	lateComment := extractFile(
		"docs.go",
		"docs.go",
		[]byte("package docs\n\n// Values generated by callers remain supported.\nconst Value = 1\n"),
		"digest",
	)
	require.False(t, lateComment.Generated)
}

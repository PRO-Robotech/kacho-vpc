// kachoctl-ipam — admin CLI для IPAM-операций kacho-vpc:
//   pool create/list/get/delete + bind* + check + explain.
//   network set-pool-selector / unset-pool-selector / get-pool-selector.
//
// Подключается к internal-порту kacho-vpc (default :9091). Использование
// внутри cluster (через port-forward) или dev-стенда.
//
// Минималистичная CLI — без cobra/spf13, чтобы не тащить лишних зависимостей.
// Если admin-tool превратится в production-grade, имеет смысл переписать на
// cobra.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	vpcv1 "github.com/PRO-Robotech/kacho-proto/gen/go/kacho/cloud/vpc/v1"
)

const usage = `kachoctl-ipam — IPAM admin CLI for kacho-vpc

USAGE:
  kachoctl-ipam [global flags] <command> [command flags]

GLOBAL FLAGS:
  -addr string    kacho-vpc internal gRPC addr (default "localhost:9091")

COMMANDS:
  pool create               Create a new AddressPool
  pool list                 List AddressPools (global resource)
  pool get                  Get a single AddressPool
  pool delete               Delete an AddressPool
  pool bind-network         Bind pool as Network's default
  pool bind-address         Bind pool as Address override
  pool unbind-network       Remove network->pool binding
  pool unbind-address       Remove address->pool binding

  cloud set-pool-selector     Set admin routing-labels на Cloud
  cloud unset-pool-selector   Remove
  cloud get-pool-selector     Get

  region create|get|list|update|delete    Manage Region (global)
  zone   create|get|list|update|delete    Manage Zone (global)

  ipam check                Diagnostic: ambiguous configurations
  ipam explain              Show resolved pool for address/network
`

func main() {
	addr := flag.String("addr", getenvOr("KACHO_VPC_INTERNAL_ADDR", "localhost:9091"), "kacho-vpc internal gRPC addr")
	flag.Usage = func() { fmt.Print(usage) }
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(2)
	}

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		die("connect %s: %v", *addr, err)
	}
	defer conn.Close()

	pools := vpcv1.NewInternalAddressPoolServiceClient(conn)
	cloudInternal := vpcv1.NewInternalCloudServiceClient(conn)
	regions := vpcv1.NewInternalRegionServiceClient(conn)
	zones := vpcv1.NewInternalZoneServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch args[0] {
	case "pool":
		runPool(ctx, pools, args[1:])
	case "cloud":
		runCloud(ctx, cloudInternal, args[1:])
	case "ipam":
		runIpam(ctx, pools, args[1:])
	case "region":
		runRegion(ctx, regions, args[1:])
	case "zone":
		runZone(ctx, zones, args[1:])
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func runRegion(ctx context.Context, c vpcv1.InternalRegionServiceClient, args []string) {
	if len(args) < 1 {
		die("region: subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "create":
		fs := flag.NewFlagSet("region create", flag.ExitOnError)
		id := fs.String("id", "", "region ID (required, e.g. ru-central1)")
		name := fs.String("name", "", "human-readable name")
		_ = fs.Parse(rest)
		if *id == "" {
			die("region create: --id required")
		}
		v, err := c.Create(ctx, &vpcv1.CreateRegionRequest{Id: *id, Name: *name})
		if err != nil {
			die("Create: %v", err)
		}
		printJSON(v)
	case "get":
		id := pickPositionalOrIDFlag("region get", rest)
		v, err := c.Get(ctx, &vpcv1.GetRegionRequest{RegionId: id})
		if err != nil {
			die("Get: %v", err)
		}
		printJSON(v)
	case "list":
		resp, err := c.List(ctx, &vpcv1.ListRegionsRequest{PageSize: 1000})
		if err != nil {
			die("List: %v", err)
		}
		printJSON(resp)
	case "update":
		fs := flag.NewFlagSet("region update", flag.ExitOnError)
		id := fs.String("id", "", "region ID (required)")
		name := fs.String("name", "", "new name")
		_ = fs.Parse(rest)
		if *id == "" {
			die("region update: --id required")
		}
		v, err := c.Update(ctx, &vpcv1.UpdateRegionRequest{RegionId: *id, Name: *name})
		if err != nil {
			die("Update: %v", err)
		}
		printJSON(v)
	case "delete":
		id := pickPositionalOrIDFlag("region delete", rest)
		if _, err := c.Delete(ctx, &vpcv1.DeleteRegionRequest{RegionId: id}); err != nil {
			die("Delete: %v", err)
		}
		fmt.Println("deleted")
	default:
		die("region: unknown subcommand %q", sub)
	}
}

func runZone(ctx context.Context, c vpcv1.InternalZoneServiceClient, args []string) {
	if len(args) < 1 {
		die("zone: subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "create":
		fs := flag.NewFlagSet("zone create", flag.ExitOnError)
		id := fs.String("id", "", "zone ID (required, e.g. ru-central1-a)")
		region := fs.String("region-id", "", "region ID (required)")
		name := fs.String("name", "", "human-readable name")
		_ = fs.Parse(rest)
		if *id == "" || *region == "" {
			die("zone create: --id and --region-id required")
		}
		v, err := c.Create(ctx, &vpcv1.CreateZoneRequest{Id: *id, RegionId: *region, Name: *name})
		if err != nil {
			die("Create: %v", err)
		}
		printJSON(v)
	case "get":
		id := pickPositionalOrIDFlag("zone get", rest)
		v, err := c.Get(ctx, &vpcv1.GetZoneRequest{ZoneId: id})
		if err != nil {
			die("Get: %v", err)
		}
		printJSON(v)
	case "list":
		fs := flag.NewFlagSet("zone list", flag.ExitOnError)
		region := fs.String("region-id", "", "filter by region ID (optional)")
		_ = fs.Parse(rest)
		resp, err := c.List(ctx, &vpcv1.ListZonesRequest{RegionId: *region, PageSize: 1000})
		if err != nil {
			die("List: %v", err)
		}
		printJSON(resp)
	case "update":
		fs := flag.NewFlagSet("zone update", flag.ExitOnError)
		id := fs.String("id", "", "zone ID (required)")
		name := fs.String("name", "", "new name")
		_ = fs.Parse(rest)
		if *id == "" {
			die("zone update: --id required")
		}
		v, err := c.Update(ctx, &vpcv1.UpdateZoneRequest{ZoneId: *id, Name: *name})
		if err != nil {
			die("Update: %v", err)
		}
		printJSON(v)
	case "delete":
		id := pickPositionalOrIDFlag("zone delete", rest)
		if _, err := c.Delete(ctx, &vpcv1.DeleteZoneRequest{ZoneId: id}); err != nil {
			die("Delete: %v", err)
		}
		fmt.Println("deleted")
	default:
		die("zone: unknown subcommand %q", sub)
	}
}

func runPool(ctx context.Context, c vpcv1.InternalAddressPoolServiceClient, args []string) {
	if len(args) < 1 {
		die("pool: subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "create":
		fs := flag.NewFlagSet("pool create", flag.ExitOnError)
		name := fs.String("name", "", "pool name")
		desc := fs.String("desc", "", "description")
		cidrs := fs.String("cidr", "", "comma-separated CIDR blocks (required)")
		kind := fs.String("kind", "EXTERNAL_PUBLIC", "EXTERNAL_PUBLIC | EXTERNAL_TEST | RESERVED_INTERNAL")
		zone := fs.String("zone-id", "", "zone ID (e.g. ru-central1-a); empty = global pool")
		isDefault := fs.Bool("is-default", false, "is the default pool for zone+kind")
		selector := fs.String("selector", "", "selector_labels: comma-separated k=v")
		priority := fs.Int("priority", 0, "selector priority (tie-break)")
		_ = fs.Parse(rest)
		if *cidrs == "" {
			die("pool create: --cidr required")
		}
		req := &vpcv1.CreateAddressPoolRequest{
			Name:             *name,
			Description:      *desc,
			CidrBlocks:       splitCSV(*cidrs),
			Kind:             parseKind(*kind),
			ZoneId:           *zone,
			IsDefault:        *isDefault,
			SelectorLabels:   parseLabels(*selector),
			SelectorPriority: int32(*priority),
		}
		p, err := c.Create(ctx, req)
		if err != nil {
			die("Create: %v", err)
		}
		printJSON(p)

	case "list":
		fs := flag.NewFlagSet("pool list", flag.ExitOnError)
		zone := fs.String("zone-id", "", "filter zone (e.g. ru-central1-a)")
		kind := fs.String("kind", "", "filter kind")
		_ = fs.Parse(rest)
		req := &vpcv1.ListAddressPoolsRequest{
			ZoneId:   *zone,
			PageSize: 1000,
		}
		if *kind != "" {
			req.Kind = parseKind(*kind)
		}
		resp, err := c.List(ctx, req)
		if err != nil {
			die("List: %v", err)
		}
		printJSON(resp)

	case "get":
		id := pickPositionalOrIDFlag("pool get", rest)
		p, err := c.Get(ctx, &vpcv1.GetAddressPoolRequest{PoolId: id})
		if err != nil {
			die("Get: %v", err)
		}
		printJSON(p)

	case "delete":
		id := pickPositionalOrIDFlag("pool delete", rest)
		if _, err := c.Delete(ctx, &vpcv1.DeleteAddressPoolRequest{PoolId: id}); err != nil {
			die("Delete: %v", err)
		}
		fmt.Println("deleted")

	case "bind-network":
		fs := flag.NewFlagSet("pool bind-network", flag.ExitOnError)
		net := fs.String("network", "", "network ID")
		pool := fs.String("pool", "", "pool ID")
		_ = fs.Parse(rest)
		if *net == "" || *pool == "" {
			die("bind-network: --network and --pool required")
		}
		if _, err := c.BindAsNetworkDefault(ctx, &vpcv1.BindAsNetworkDefaultRequest{
			NetworkId: *net, PoolId: *pool,
		}); err != nil {
			die("BindAsNetworkDefault: %v", err)
		}
		fmt.Println("bound")

	case "unbind-network":
		fs := flag.NewFlagSet("pool unbind-network", flag.ExitOnError)
		net := fs.String("network", "", "network ID")
		_ = fs.Parse(rest)
		if *net == "" {
			die("unbind-network: --network required")
		}
		if _, err := c.UnbindNetworkDefault(ctx, &vpcv1.UnbindNetworkDefaultRequest{NetworkId: *net}); err != nil {
			die("UnbindNetworkDefault: %v", err)
		}
		fmt.Println("unbound")

	case "bind-address":
		fs := flag.NewFlagSet("pool bind-address", flag.ExitOnError)
		addr := fs.String("address", "", "address ID")
		pool := fs.String("pool", "", "pool ID")
		_ = fs.Parse(rest)
		if *addr == "" || *pool == "" {
			die("bind-address: --address and --pool required")
		}
		if _, err := c.BindAsAddressOverride(ctx, &vpcv1.BindAsAddressOverrideRequest{
			AddressId: *addr, PoolId: *pool,
		}); err != nil {
			die("BindAsAddressOverride: %v", err)
		}
		fmt.Println("bound")

	case "unbind-address":
		fs := flag.NewFlagSet("pool unbind-address", flag.ExitOnError)
		addr := fs.String("address", "", "address ID")
		_ = fs.Parse(rest)
		if *addr == "" {
			die("unbind-address: --address required")
		}
		if _, err := c.UnbindAddressOverride(ctx, &vpcv1.UnbindAddressOverrideRequest{AddressId: *addr}); err != nil {
			die("UnbindAddressOverride: %v", err)
		}
		fmt.Println("unbound")

	default:
		die("pool: unknown subcommand %q", sub)
	}
}

// runCloud — admin set/get/unset pool-selector на Cloud (replaces network).
// External Address не имеет network_id, поэтому селектор переехал на Cloud.
func runCloud(ctx context.Context, c vpcv1.InternalCloudServiceClient, args []string) {
	if len(args) < 1 {
		die("cloud: subcommand required")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "set-pool-selector":
		fs := flag.NewFlagSet("cloud set-pool-selector", flag.ExitOnError)
		cl := fs.String("cloud", "", "cloud ID")
		selector := fs.String("selector", "", "k=v,k=v")
		setBy := fs.String("set-by", "kachoctl", "audit set_by")
		_ = fs.Parse(rest)
		if *cl == "" {
			die("set-pool-selector: --cloud required")
		}
		if _, err := c.SetPoolSelector(ctx, &vpcv1.SetCloudPoolSelectorRequest{
			CloudId:  *cl,
			Selector: parseLabels(*selector),
			SetBy:    *setBy,
		}); err != nil {
			die("SetPoolSelector: %v", err)
		}
		fmt.Println("set")

	case "unset-pool-selector":
		fs := flag.NewFlagSet("cloud unset-pool-selector", flag.ExitOnError)
		cl := fs.String("cloud", "", "cloud ID")
		_ = fs.Parse(rest)
		if *cl == "" {
			die("unset-pool-selector: --cloud required")
		}
		if _, err := c.UnsetPoolSelector(ctx, &vpcv1.UnsetCloudPoolSelectorRequest{CloudId: *cl}); err != nil {
			die("UnsetPoolSelector: %v", err)
		}
		fmt.Println("unset")

	case "get-pool-selector":
		fs := flag.NewFlagSet("cloud get-pool-selector", flag.ExitOnError)
		cl := fs.String("cloud", "", "cloud ID")
		_ = fs.Parse(rest)
		if *cl == "" {
			die("get-pool-selector: --cloud required")
		}
		resp, err := c.GetPoolSelector(ctx, &vpcv1.GetCloudPoolSelectorRequest{CloudId: *cl})
		if err != nil {
			die("GetPoolSelector: %v", err)
		}
		printJSON(resp)

	default:
		die("cloud: unknown subcommand %q", sub)
	}
}

func runIpam(ctx context.Context, c vpcv1.InternalAddressPoolServiceClient, args []string) {
	if len(args) < 1 {
		die("ipam: subcommand required")
	}
	switch args[0] {
	case "check":
		fs := flag.NewFlagSet("ipam check", flag.ExitOnError)
		zone := fs.String("zone", "", "filter to zone")
		_ = fs.Parse(args[1:])
		resp, err := c.Check(ctx, &vpcv1.CheckRequest{ZoneId: *zone})
		if err != nil {
			die("Check: %v", err)
		}
		if len(resp.GetWarnings()) == 0 {
			fmt.Println("OK: no warnings")
			return
		}
		for _, w := range resp.GetWarnings() {
			fmt.Println("WARN:", w)
		}

	case "explain":
		fs := flag.NewFlagSet("ipam explain", flag.ExitOnError)
		addr := fs.String("address", "", "address ID")
		net := fs.String("network", "", "network ID")
		_ = fs.Parse(args[1:])
		resp, err := c.ExplainResolution(ctx, &vpcv1.ExplainResolutionRequest{
			AddressId: *addr, NetworkId: *net,
		})
		if err != nil {
			die("ExplainResolution: %v", err)
		}
		printJSON(resp)

	default:
		die("ipam: unknown subcommand %q", args[0])
	}
}

// pickPositionalOrIDFlag — позволяет указывать ID либо positional (`pool delete <id>`),
// либо через `--id <id>`. Положительная DX-фича для частых команд get/delete.
func pickPositionalOrIDFlag(cmdName string, rest []string) string {
	fs := flag.NewFlagSet(cmdName, flag.ExitOnError)
	id := fs.String("id", "", "pool ID (либо как positional argument: "+cmdName+" <id>)")
	_ = fs.Parse(rest)
	if *id != "" {
		return *id
	}
	if fs.NArg() >= 1 && fs.Arg(0) != "" {
		return fs.Arg(0)
	}
	die("%s: ID required (positional or --id)", cmdName)
	return ""
}

// --- helpers ---

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func parseLabels(s string) map[string]string {
	if s == "" {
		return nil
	}
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			die("invalid label %q (expected k=v)", kv)
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out
}

func parseKind(s string) vpcv1.AddressPoolKind {
	switch strings.ToUpper(s) {
	case "EXTERNAL_PUBLIC":
		return vpcv1.AddressPoolKind_EXTERNAL_PUBLIC
	case "EXTERNAL_TEST":
		return vpcv1.AddressPoolKind_EXTERNAL_TEST
	case "RESERVED_INTERNAL":
		return vpcv1.AddressPoolKind_RESERVED_INTERNAL
	case "":
		return vpcv1.AddressPoolKind_ADDRESS_POOL_KIND_UNSPECIFIED
	default:
		die("unknown kind %q", s)
		return 0
	}
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		die("marshal: %v", err)
	}
	fmt.Println(string(b))
}

func getenvOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kachoctl-ipam: "+format+"\n", args...)
	os.Exit(1)
}

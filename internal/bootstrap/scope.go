package bootstrap

import (
	"sync"

	kernelscope "github.com/omwk/go-admin-api/internal/kernel/scope"
	identityscope "github.com/omwk/go-admin-api/internal/modules/identity/scope"
	shopscope "github.com/omwk/go-admin-api/internal/modules/shop/scope"
)

var scopeRegistry = sync.OnceValues(func() (*kernelscope.Registry, error) {
	return kernelscope.NewRegistry(identityscope.Declarations(), shopscope.Declarations(), kernelscope.Set{Queries: map[string]kernelscope.QueryDeclaration{
		"Schedule.ListEnabledJobs":                        {Policy: "bootstrap-only enabled schedule projection"},
		"Sync.ResolveTask":                                {Policy: "bootstrap-only verified task resolution"},
		"SyncEngine.ResolveJob":                           {Policy: "bootstrap-only enabled job projection"},
		"SyncEngine.CreateRun":                            {Policy: "authorized human or trusted cron bootstrap"},
		"SyncEngine.LoadWork":                             {Policy: "bootstrap-only live run identity validation"},
		"SyncEngine.LoadCursor":                           {Policy: "work capability exact checkpoint"},
		"SyncEngine.CommitPage":                           {Policy: "work capability page transaction"},
		"SyncEngine.DeferRun":                             {Policy: "work capability exact run retry"},
		"SyncAdmin.Authorize":                             {Policy: "reload live human authorization"},
		"SyncAdmin.ListDrifts":                            {Policy: "human shop scope including count"},
		"SyncAdmin.ChangeDrift":                           {Policy: "human shop scope atomic audited disposition"},
		"SyncAdmin.PromoteBaseline":                       {Policy: "human tenant and sample shop scope atomic audited promotion"},
		"SyncAdmin.ListRuns":                              {Policy: "human shop scope including count"},
		"SyncAdmin.ReadRun":                               {Policy: "human shop scope exact run"},
		"SyncAdmin.ListRecords":                           {Policy: "human shop scope including count"},
		"SyncAdmin.ReplaySource":                          {Policy: "human shop scope replay metadata"},
		"SyncReplay.LoadReplay":                           {Policy: "work capability fixed run replay raw"},
		"SyncMetrics.Snapshot":                            {Policy: "bootstrap-only fixed system aggregate projection"},
		"Catalog.ListCategories":                          {Policy: "tenant-scoped category projection"},
		"Catalog.ListCategoryMappingPool":                 {Policy: "tenant scoped mapping projection with platform and matched filters"},
		"Catalog.SetManualCategoryMapping":                {Policy: "tenant-scoped mapping and category validation"},
		"Catalog.ListInventories":                         {Policy: "tenant scoped inventory snapshots with verified shop scope"},
		"Catalog.CleanupInventorySnapshots":               {Policy: "leader-only retention cleanup before fixed cutoff"},
		"Catalog.ListProducts":                            {Policy: "tenant-scoped SPU projection with indexed summary filters"},
		"Catalog.Product":                                 {Policy: "tenant-scoped SPU detail without raw metadata"},
		"Catalog.ListListings":                            {Policy: "tenant-scoped sanitized listing projection"},
		"Catalog.CreateSPU":                               {Policy: "tenant-scoped manual SPU creation"},
		"Catalog.UpdateSPU":                               {Policy: "tenant-scoped manual SPU version update"},
		"Catalog.ArchiveSPU":                              {Policy: "tenant-scoped SPU soft archive"},
		"Catalog.ReplaceManualSKUs":                       {Policy: "tenant-scoped manual SKU replacement and mapping detach"},
		"Catalog.MergeListings":                           {Policy: "tenant-scoped listing merge and SKU binding"},
		"Catalog.UnmergeListing":                          {Policy: "tenant-scoped listing unmerge and SKU detach"},
		"Catalog.RecomputeSPUSummary":                     {Policy: "tenant-scoped transactional SPU summary update"},
		"MappingPublish.StatusSnapshot":                   {Policy: "verified human tenant status table projection"},
		"MappingPublish.DryRunSamples":                    {Policy: "human shop scope bounded read-only samples"},
		"MappingPublish.CASActive":                        {Policy: "verified human tenant draft persist then transactional profile CAS"},
		"CredentialUse.WorkerCredential":                  {Policy: "worker-only active credential projection"},
		"CredentialUse.AuditCredentialUse":                {Policy: "worker-only system credential audit"},
		"Admin.EmitAuthorizationInvalidation":             {Policy: "transactional authorization invalidation outbox"},
		"PermissionRelease.EmitAuthorizationInvalidation": {Policy: "transactional authorization invalidation outbox"},
		"PermissionRelease.RegisterPermissions":           {Policy: "transactional registry upsert for approved permission release"},
	}})
})

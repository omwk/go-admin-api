function New-SmokeShop {
    $p = Invoke-SmokeSQL @'
WITH inserted AS (INSERT INTO platform(code,name,auth_type,capabilities,config_schema)
VALUES('mock','S3 mock','api_key','{"resources":["product","order"],"incremental_modes":["page","timestamp"]}','{}') RETURNING id)
SELECT jsonb_build_object('id',id) FROM inserted
'@
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/admin/shops' @{ platform_id = $p.id; name = 'S3 mock shop'; external_shop_id = $script:Schema; timezone = 'UTC'; currency = 'CNY' } -Expected 201 -Label 'create shop'
    $script:ShopID = $r.body.data.id
    Assert-Smoke ($script:ShopID -gt 0 -and $r.body.data.platform_id -eq $p.id) 'created shop identity mismatch'
    $script:ChildEnv.SMOKE_SHOP_ID = [string]$script:ShopID
    $r = Invoke-SmokeHTTP 'POST' "/api/v1/admin/shops/$script:ShopID/credentials" @{ kind = 'api_key'; label = 'smoke'; values = @{ private_value = $script:Credential } } -Expected 201 -Label 'write credential'
    Assert-Smoke ($r.body.data.status -eq 'active') 'credential is not active'
    $r = Invoke-SmokeHelper 'credential'
    Assert-Smoke ($r.encrypted -and $r.decrypted_matches) 'stored credential cannot decrypt to its original value'
}

function Start-SmokeSync {
    $jobs = Invoke-SmokeHelper 'fixtures'
    foreach ($resource in @('product', 'order')) {
        $job = $jobs.$resource
        $r = Invoke-SmokeHTTP 'POST' "/api/v1/admin/sync-jobs/$job/run" @{} -Expected 201 -Label "start $resource sync"
        $run = $r.body.data.id
        Assert-Smoke ($run -gt 0) "$resource sync missing run ID"
        Wait-Smoke {
            Assert-Smoke (-not $script:Worker.Process.HasExited) 'worker exited during sync'
            $r = Invoke-SmokeHTTP 'GET' "/api/v1/admin/sync-runs/$run" -Label "observe $resource sync"
            $status = $r.body.data.status
            Assert-Smoke ($status -in @('running', 'success')) "$resource sync ended with status $status"
            return ($status -eq 'success')
        } "$resource sync did not succeed"
    }
}

function Test-SmokeData {
    $r = Invoke-SmokeSQL @'
SELECT jsonb_build_object('listing',(SELECT count(*) FROM listing),'spu',(SELECT count(*) FROM product_spu),
'order',(SELECT count(*) FROM sales_order),'skus',(SELECT count(*) FROM listing_sku),
'identities',(SELECT jsonb_agg(external_product_id ORDER BY external_product_id) FROM listing),
'orders',(SELECT jsonb_agg(external_order_id ORDER BY external_order_id) FROM sales_order))
'@
    # normal.json produces two unmerged listings and one SKU; order.json has two orders.
    Assert-Smoke ($r.listing -eq 2 -and $r.spu -eq 0 -and $r.order -eq 2 -and $r.skus -eq 1) 'mock persistence counts differ from fixtures (listing=2, SPU=0, orders=2, listing_sku=1)'
    Assert-Smoke (($r.identities -join ',') -eq 'p-1,p-2') 'persisted product fixture identities differ'
    Assert-Smoke (($r.orders -join ',') -eq 'mock-order-1,mock-order-2') 'persisted order fixture identities differ'
}

function Merge-SmokeListings {
    $r = Invoke-SmokeHTTP 'GET' "/api/v1/admin/listings?shop_id=$script:ShopID" -Label 'list ingested products'
    Assert-Smoke ($r.body.data.total -eq 2) 'expected two merge candidates'
    $ids = @($r.body.data.list | ForEach-Object { $_.id })
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/admin/products' @{ title = 'S3 merged product'; status = 'active' } -Expected 201 -Label 'create merge target'
    $script:SPUID = $r.body.data.product.id
    Assert-Smoke ($script:SPUID -gt 0) 'merge target missing SPU ID'
    $r = Invoke-SmokeHTTP 'POST' "/api/v1/admin/products/$script:SPUID/merge" @{ listing_ids = $ids } -Label 'merge both listings'
    $product = $r.body.data.product
    Assert-Smoke ($r.body.data.listings.Count -eq 2 -and $r.body.data.skus.Count -eq 1) 'merge membership/SKU count mismatch'
    Assert-Smoke ($product.price_min_cents -eq 1234 -and $product.price_max_cents -eq 1234 -and $product.sku_count -eq 1) 'merge denormalized summary mismatch'
    $script:SKUID = $r.body.data.skus[0].id
    $row = Invoke-SmokeSQL "SELECT jsonb_build_object('bound',count(*),'spus',count(DISTINCT spu_id)) FROM listing WHERE spu_id=`$1" @($script:SPUID)
    Assert-Smoke ($row.bound -eq 2 -and $row.spus -eq 1) 'merge was not durably persisted'
}

function New-SmokeQuote {
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/admin/customers' @{ code = 's3'; name = 'Synthetic smoke customer'; status = 'active' } -Expected 201 -Label 'create customer'
    $script:CustomerID = $r.body.data.id
    Assert-Smoke ($script:CustomerID -gt 0) 'customer missing ID'
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/admin/price-lists' @{ type = 'customer'; customer_id = $script:CustomerID; name = 'S3 quote'; currency = 'CNY'; status = 'active' } -Expected 201 -Label 'create customer price list'
    $script:PriceListID = $r.body.data.id
    Assert-Smoke ($r.body.data.customer_id -eq $script:CustomerID) 'quote customer mismatch'
    $r = Invoke-SmokeHTTP 'POST' "/api/v1/admin/price-lists/$script:PriceListID/items:batch" @{ version = $r.body.data.version; items = @(@{ spu_id = $script:SPUID; sku_id = $script:SKUID; price_cents = 777; moq = 1 }) } -Label 'write exclusive price'
    Assert-Smoke ($r.body.data.success -eq 1 -and @($r.body.data.failed).Count -eq 0) 'exclusive price batch did not commit one item'
}

function New-SmokeLink {
    $script:SharePassword = 'Sm0ke-' + [Guid]::NewGuid().ToString('N')
    $script:Secrets.Add($script:SharePassword)
    $expires = [DateTime]::UtcNow.AddHours(1).ToString('yyyy-MM-ddTHH:mm:ssZ')
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/admin/share-links' @{ title = 'S3 frozen quote'; customer_id = $script:CustomerID;
        price_list_id = $script:PriceListID; currency = 'CNY'; mode = 'snapshot'; password = $script:SharePassword;
        expires_at = $expires; allow_inquiry = $true; items = @(@{ spu_id = $script:SPUID; sku_ids = @($script:SKUID) }) } -Expected 201 -Label 'create password link'
    $script:LinkID = $r.body.data.id
    $script:SharePath = $r.body.data.url
    Assert-Smoke ($script:LinkID -gt 0 -and $r.body.data.secret_delivered -and $script:SharePath -match '^/api/v1/share/v1[A-Za-z0-9_-]{22}$') 'link receipt missing valid one-time secret URL'
    $script:Secrets.Add($script:SharePath)
    $row = Invoke-SmokeSQL "SELECT jsonb_build_object('password',password_hash IS NOT NULL,'expires',expires_at,'status',status) FROM share_link WHERE id=`$1" @($script:LinkID)
    Assert-Smoke ($row.password -and $row.status -eq 'active' -and [DateTime]::Parse($row.expires).ToUniversalTime() -gt [DateTime]::UtcNow) 'link password/expiry was not persisted'
}

function Test-SmokeBrowse {
    Invoke-SmokeHTTP 'GET' $script:SharePath -Expected 401 -Code 70103 -Anonymous $true -Label 'deny missing password' | Out-Null
    $r = Invoke-SmokeHTTP 'POST' ($script:SharePath + '/verify') @{ password = $script:SharePassword } -Anonymous $true -Label 'verify link password'
    Assert-Smoke ($r.body.data.verified -eq $true) 'password verification not acknowledged'
    Set-SmokeCookie $r
    $r = Invoke-SmokeHTTP 'GET' $script:SharePath -Anonymous $true -Browser $true -Label 'browse frozen quote'
    Set-SmokeCookie $r
    Assert-Smoke ($r.body.data.items.Count -eq 1 -and $r.body.data.items[0].display_price_cents -eq 777) 'anonymous price differs from exclusive quote'
    Assert-Smoke ($r.body.data.mode -eq 'snapshot' -and $r.body.data.currency -eq 'CNY') 'anonymous snapshot terms mismatch'
    $script:AdmittedAt = [DateTime]::UtcNow
}

function New-SmokeInquiry {
    # The public admission contract requires a two-second browser dwell time.
    Wait-Smoke { ([DateTime]::UtcNow - $script:AdmittedAt).TotalSeconds -ge 2 } 'browser admission dwell time exceeded step budget'
    $script:ContactName = 'synthetic-' + [Guid]::NewGuid().ToString('N')
    $script:ContactPhone = 'synthetic-' + [Guid]::NewGuid().ToString('N')
    $script:Secrets.Add($script:ContactName); $script:Secrets.Add($script:ContactPhone)
    $r = Invoke-SmokeHTTP 'POST' ($script:SharePath + '/inquiry') @{ contact_name = $script:ContactName; contact_phone = $script:ContactPhone;
        items = @(@{ spu_id = $script:SPUID; sku_id = $script:SKUID; qty = 2; note = '' }) } -Expected 201 -Anonymous $true -Browser $true -Label 'submit inquiry'
    Assert-Smoke ($r.body.data.submitted -eq $true) 'inquiry not acknowledged'
    $r = Invoke-SmokeSQL @'
SELECT jsonb_build_object('id',id,'encrypted',octet_length(contact_name_cipher)>16 AND octet_length(contact_phone_cipher)>16
AND octet_length(contact_name_nonce)>0 AND octet_length(contact_phone_nonce)>0,'cleartext_absent',
position($1 in to_jsonb(i)::text)=0 AND position($2 in to_jsonb(i)::text)=0,
'inbox',(SELECT count(*) FROM notification WHERE channel='inbox')) FROM inquiry i
'@ @($script:ContactName, $script:ContactPhone)
    $script:InquiryID = $r.id
    Assert-Smoke ($r.id -gt 0 -and $r.encrypted -and $r.cleartext_absent) 'inquiry DB envelope/plaintext assertion failed'
    Assert-Smoke ($r.inbox -eq 1) 'inquiry notification was not persisted'
}

function Test-SmokeDisclosure {
    Invoke-SmokeHTTP 'GET' "/api/v1/admin/inquiries/$script:InquiryID/pii" -Expected 403 -Code 20108 -Label 'deny before reauthentication' | Out-Null
    $r = Invoke-SmokeHTTP 'POST' '/api/v1/auth/reauth' @{ password = $script:Password } -Label 'reauthenticate administrator'
    Assert-Smoke ([DateTime]::Parse($r.body.data.expires_at).ToUniversalTime() -gt [DateTime]::UtcNow) 'reauthentication window not active'
    $r = Invoke-SmokeHTTP 'GET' "/api/v1/admin/inquiries/$script:InquiryID/pii" -Label 'read decrypted inquiry'
    Assert-Smoke ($r.body.data.contact_name -eq $script:ContactName -and $r.body.data.contact_phone -eq $script:ContactPhone) 'decrypted inquiry differs from submission'
    $row = Invoke-SmokeSQL "SELECT jsonb_build_object('success',count(*) FILTER(WHERE result='success'),'failure',count(*) FILTER(WHERE result='failure')) FROM audit_log WHERE action='share.inquiry.pii.read' AND resource_id=`$1" @([string]$script:InquiryID)
    Assert-Smoke ($row.success -eq 1 -and $row.failure -eq 1) 'PII disclosure success/denial audits not persisted'
}

function Revoke-SmokeLink {
    if ($Fault -ne 'SkipRevoke') {
        Invoke-SmokeHTTP 'POST' "/api/v1/admin/share-links/$script:LinkID/revoke" @{} -Expected 204 -Label 'revoke quote link' | Out-Null
    } else { Write-Host '  Fault injection: deliberately omit revocation' }
    Invoke-SmokeHTTP 'GET' $script:SharePath -Expected 404 -Code 70102 -Anonymous $true -Browser $true -Label 'deny cached browser after revocation' | Out-Null
    $row = Invoke-SmokeSQL "SELECT jsonb_build_object('status',status,'revoked',revoked_at IS NOT NULL) FROM share_link WHERE id=`$1" @($script:LinkID)
    Assert-Smoke ($row.status -eq 'revoked' -and $row.revoked) 'revocation not durable'
}

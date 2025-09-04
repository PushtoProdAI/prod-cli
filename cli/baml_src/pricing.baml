class ServicePricing {
    service_name string
    service_type string
    plan string
    monthly_cost float
}

class PricingResponse {
    services ServicePricing[]
    total_cost float
}

function FetchRenderPricing(services: string) -> PricingResponse {
    client "OllamaClient"
    prompt #"
        {{ _.role("system") }}
        You are a pricing expert that fetches current pricing from cloud providers.
        
        Your task is to:
        1. Fetch current pricing information from https://render.com/pricing
        2. Extract pricing for the specific services requested
        3. Return accurate monthly costs in USD
        
        For Render services, typical pricing includes:
        - Web services: Free ($0), Starter ($7), Standard ($25), Pro ($85), Pro Plus ($175), Pro Max ($225), Pro Ultra ($450)
        - PostgreSQL databases: Basic 256MB ($7), Basic 1GB ($20), Standard 2GB ($50), Pro 4GB ($120), Pro 8GB ($200), Pro 16GB ($350)
        - Redis: Standard ($25), Pro ($85), Pro Plus ($175)
        - Storage: $0.25 per GB per month
        
        Important: Only return actual current pricing from the website. If you cannot fetch current data, clearly indicate that in your response.
        
        {{ _.role("user") }}
        Please fetch current pricing from https://render.com/pricing for these services and return the monthly costs:
        
        {{ services }}
        
        Return the response in the exact format specified by the schema. Include:
        - service_name: name of the service (e.g., "web", "postgresql", "redis")
        - service_type: provider type (e.g., "web", "postgresql", "redis") 
        - plan: the specific plan (e.g., "standard", "basic_256mb")
        - monthly_cost: the monthly cost in USD as a number
        
        Calculate total_cost as the sum of all service costs.
        
        {{ ctx.output_format }}
    "#
}

function FetchFlyioPricing(services: string) -> PricingResponse {
    client "OllamaClient"
    prompt #"
        {{ _.role("system") }}
        You are a pricing expert that fetches current pricing from cloud providers.
        
        Your task is to:
        1. Fetch current pricing information from https://fly.io/docs/pricing/
        2. Extract pricing for the specific services requested
        3. Return accurate monthly costs in USD
        
        For Fly.io services, typical pricing includes:
        - Machines: shared-cpu-1x ($5.70), shared-cpu-2x ($11.40), shared-cpu-4x ($22.80), shared-cpu-8x ($45.60), performance-1x ($62.00), performance-2x ($124.00), performance-4x ($248.00), performance-8x ($496.00)
        - PostgreSQL databases: db-shared-1 ($7.00), db-dedicated-1 ($27.00), db-dedicated-2 ($54.00), db-dedicated-4 ($108.00)
        - Redis: redis-shared ($5.00), redis-dedicated ($15.00)
        - Storage: $0.15 per GB per month
        
        Important: Only return actual current pricing from the website. If you cannot fetch current data, clearly indicate that in your response.
        
        {{ _.role("user") }}
        Please fetch current pricing from https://fly.io/docs/pricing/ for these services and return the monthly costs:
        
        {{ services }}
        
        Return the response in the exact format specified by the schema. Include:
        - service_name: name of the service (e.g., "web", "postgresql", "redis")
        - service_type: provider type (e.g., "web", "postgresql", "redis") 
        - plan: the specific plan (e.g., "shared-cpu-1x", "db-shared-1")
        - monthly_cost: the monthly cost in USD as a number
        
        Calculate total_cost as the sum of all service costs.
        
        {{ ctx.output_format }}
    "#
}

function FetchNetlifyPricing(services: string) -> PricingResponse {
    client "OllamaClient"
    prompt #"
        {{ _.role("system") }}
        You are a pricing expert that fetches current pricing from cloud providers.
        
        Your task is to:
        1. Fetch current pricing information from https://www.netlify.com/pricing/
        2. Extract pricing for the specific services requested
        3. Return accurate monthly costs in USD
        
        IMPORTANT: Netlify has updated to a new credit-based pricing system as of September 2025:
        
        NEW PLANS:
        - Free: $0 forever (300 credits/month included)
        - Personal: $9/month (1,000 credits/month included)
        - Pro: $20 per seat/month (5,000 credits/month included)
        - Enterprise: Custom pricing
        
        CREDIT USAGE:
        - Production deploys: 15 credits each
        - Compute: 5 credits per GB-hour
        - Form submissions: 1 credit each
        - Bandwidth: 10 credits per GB
        - Web requests: 3 credits per 10k requests
        
        For static sites on the Free plan, the cost is $0. For any service marked as "free" plan, the cost should be $0.
        
        OLD PLANS (legacy customers only):
        - Static sites: Free ($0), Pro ($19), Business ($99), Enterprise (custom pricing)
        - Functions: Free tier (125K invocations), Pro ($19 includes 500K invocations), Business ($99 includes 2M invocations)
        - Forms: Free tier (100 submissions), Pro ($19 includes 1K submissions), Business ($99 includes 10K submissions)
        - Background functions: Pro ($19 includes 500K invocations), Business ($99 includes 2M invocations)
        - Large media: Pro ($19 includes 1TB), Business ($99 includes 5TB)
        
        Important: Only return actual current pricing from the website. If you cannot fetch current data, clearly indicate that in your response.
        
        {{ _.role("user") }}
        Please fetch current pricing from https://www.netlify.com/pricing/ for these services and return the monthly costs:
        
        {{ services }}
        
        Return the response in the exact format specified by the schema. Include:
        - service_name: name of the service (e.g., "static-site", "functions", "forms")
        - service_type: provider type (e.g., "netlify", "static-site", "functions") 
        - plan: the specific plan (e.g., "free", "pro", "business")
        - monthly_cost: the monthly cost in USD as a number
        
        Calculate total_cost as the sum of all service costs.
        
        {{ ctx.output_format }}
    "#
}
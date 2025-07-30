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
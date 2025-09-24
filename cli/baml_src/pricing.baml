
class Service {
    name string
    type string
    plan string
    storage string
}

class ServicePricing {
    service_name string
    service_type string
    plan string
    monthly_cost float
    usage_cost UsageCost[]?
}

class PricingResponse {
    services ServicePricing[]
    total_cost float
}

class UsageCost {
    unit string
    cost_per_unit float
}



function FetchPricing(service: Service, content: string) -> ServicePricing {
    client "ProxyClient"
    prompt #"
{{ _.role("system") }}
You are a pricing expert. You will be given markdown content extracted from a provider’s pricing page. 
Use only the provided markdown — do not fetch external data or make assumptions.

Your task is to:
1. Extract pricing for the specific service requested.
2. Match the service’s plan to the corresponding plan in the pricing markdown (case-insensitive, allow fuzzy matching).
3. If the service cannot be matched, return monthly_cost as -1.
4. Always return exactly one object that follows the schema provided. Do not add fields or free text.

{{ _.role("user") }}

Service requested (JSON):
{{ service }}

Pricing Markdown:
{{ content }}

Instructions:
- Example: "web" refers to **Web Services**
- Example: "postgresql" refers to **PostgreSQL databases**
- Example: "redis" refers to **Redis instances**
- Do not guess or assume beyond the given markdown.
- Return exactly one JSON object following the schema.

Schema fields:
- service_name: human-friendly service name (e.g., "Web Service", "PostgreSQL")
- service_type: provider type (one of: "web", "postgresql", "redis")
- plan: the specific plan (e.g., "standard", "basic_256mb")
- monthly_cost: monthly cost in USD, number only (e.g., 19.0), or -1 if not found
- usage_cost:  
  - Always output as an object.  
  - If no usage-based pricing exists, output:  
    { "unit": "", "cost_per_unit": 0.0 }  
  - If usage-based pricing exists, output a fully valid object with both fields:
    - unit: string, the measurable unit (normalize to singular lowercase: "gb", "request", "minute")  
    - cost_per_unit: float (numeric value only, e.g., 0.10 for $0.10 per unit)


Important:
- Normalize units to singular lowercase consistently.  
- Always include `usage_cost` (object, even if empty/default).  

Return strictly in this JSON format:

{
  "service_name": "Web Service",
  "service_type": "web",
  "plan": "pro",
  "monthly_cost": 20.0,
  "usage_cost": { "unit": "gb", "cost_per_unit": 0.09 }
}
{{ ctx.output_format }}
    "#
}


// Supabase Edge Function: base-images
// GET /functions/v1/base-images
// Returns a mapping of languages to their ECR base image URLs

import "jsr:@supabase/functions-js/edge-runtime.d.ts"
import { createClient } from "https://esm.sh/@supabase/supabase-js@2"
import { initSentry, captureException, flushSentry } from '../_shared/sentry.ts'

// Initialize Sentry
initSentry()

interface BaseImage {
  language: string
  image_url: string
}

interface BaseImageResponse {
  [language: string]: string
}

Deno.serve(async (req: Request) => {
  if (req.method !== "GET") {
    return new Response(JSON.stringify({ error: "Method not allowed" }), {
      status: 405,
      headers: { "Content-Type": "application/json" },
    })
  }

  try {
    // Initialize Supabase client with service role key
    const supabaseUrl = Deno.env.get("SUPABASE_URL")!
    const supabaseServiceKey = Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!

    const supabase = createClient(supabaseUrl, supabaseServiceKey)

    // Fetch active base images from public.base_images
    const { data: baseImages, error } = await supabase
      .from<BaseImage>("base_images")
      .select("language, image_url")
      .eq("is_active", true)
      .order("language")

    if (error) {
      console.error("Database error:", error)
      captureException(new Error(error.message), {
        function: 'base-images',
        operation: 'database_query',
        error_details: error
      })
      return new Response(
        JSON.stringify({ error: "Failed to fetch base images" }),
        {
          status: 500,
          headers: { "Content-Type": "application/json" },
        }
      )
    }

    const response: BaseImageResponse = {}
    baseImages?.forEach((image) => {
      response[image.language] = image.image_url
    })

    return new Response(JSON.stringify(response, null, 2), {
      status: 200,
      headers: {
        "Content-Type": "application/json",
        "Cache-Control": "public, max-age=300, s-maxage=600",
        ETag: `"${Date.now()}"`,
      },
    })
  } catch (error) {
    console.error("Function error:", error)
    captureException(error instanceof Error ? error : new Error(String(error)), {
      function: 'base-images',
      operation: 'general_error'
    })
    await flushSentry()
    
    return new Response(
      JSON.stringify({
        error: "Internal server error",
        message: error instanceof Error ? error.message : "Unknown error",
      }),
      {
        status: 500,
        headers: { "Content-Type": "application/json" },
      }
    )
  }
})


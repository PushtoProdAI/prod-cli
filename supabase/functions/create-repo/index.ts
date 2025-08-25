import 'jsr:@supabase/functions-js/edge-runtime.d.ts';
import { createClient } from 'jsr:@supabase/supabase-js@2';
import { checkAndCreateECRRepo } from '../_shared/aws.ts';

Deno.serve(async (req) => {
  if (req.method !== 'POST') {
    return new Response(JSON.stringify({ error: 'Method not allowed' }), {
      status: 405,
      headers: { 'Content-Type': 'application/json' },
    });
  }
  const supabaseClient = createClient(
    Deno.env.get('SUPABASE_URL') ?? '',
    Deno.env.get('SUPABASE_ANON_KEY') ?? '',
    {
      global: {
        headers: { Authorization: req.headers.get('Authorization')! },
      },
    },
  );

  const authHeader = req.headers.get('Authorization')!;
  const token = authHeader.replace('Bearer ', '');
  const { data } = await supabaseClient.auth.getUser(token);

  const { name } = await req.json();
  if (!name) {
    return new Response("Missing name", { status: 400 });
  }

  const roleArn = Deno.env.get('AWS_ECR_ROLE_ARN')!;
  const result = await checkAndCreateECRRepo(data.user.id, name, roleArn);

  if (result instanceof Error) {
    console.error("Create repo error:", result);
    return new Response(result.message, { status: 500 });
  }

  return Response.json(result);
});

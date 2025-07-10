-- This is an initial migration file for your Supabase project
-- Add your database schema changes here

-- Example: Create a simple table
-- CREATE TABLE IF NOT EXISTS public.example_table (
--   id UUID DEFAULT gen_random_uuid() PRIMARY KEY,
--   name TEXT NOT NULL,
--   description TEXT,
--   created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
--   updated_at TIMESTAMP WITH TIME ZONE DEFAULT now()
-- );

-- Example: Enable Row Level Security (RLS)
-- ALTER TABLE public.example_table ENABLE ROW LEVEL SECURITY;

-- Example: Create a policy
-- CREATE POLICY "Users can view their own data" ON public.example_table
--   FOR SELECT USING (auth.uid() = user_id);

-- Example: Create a function to automatically update the updated_at column
-- CREATE OR REPLACE FUNCTION public.handle_updated_at()
-- RETURNS TRIGGER AS $$
-- BEGIN
--   NEW.updated_at = now();
--   RETURN NEW;
-- END;
-- $$ LANGUAGE plpgsql;

-- Example: Create a trigger to automatically update updated_at
-- CREATE TRIGGER handle_updated_at
--   BEFORE UPDATE ON public.example_table
--   FOR EACH ROW
--   EXECUTE FUNCTION public.handle_updated_at(); 
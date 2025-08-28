-- Create the table in public schema
CREATE TABLE public.base_images (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    language VARCHAR(50) NOT NULL,
    image_url TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    is_active BOOLEAN DEFAULT true
);

-- Ensure only one active image per language (partial unique constraint)
CREATE UNIQUE INDEX unique_active_language 
    ON public.base_images(language) 
    WHERE is_active = true;

-- Indexes for performance
CREATE INDEX idx_base_images_language 
    ON public.base_images(language);

CREATE INDEX idx_base_images_active 
    ON public.base_images(is_active);

-- Insert initial base images (replace for production as needed)
INSERT INTO public.base_images (language, image_url) VALUES
('node', 'public.ecr.aws/v2d8m9k7/prod:node-18-alpine'),
('nodejs', 'public.ecr.aws/v2d8m9k7/prod:node-18-alpine'),
('javascript', 'public.ecr.aws/v2d8m9k7/prod:node-18-alpine'),
('python', 'public.ecr.aws/v2d8m9k7/prod:python-3-11-slim'),
('go', 'public.ecr.aws/v2d8m9k7/prod:golang-1-21-alpine'),
('golang', 'public.ecr.aws/v2d8m9k7/prod:golang-1-21-alpine');

-- Add an updated_at trigger
CREATE OR REPLACE FUNCTION public.update_base_images_updated_at()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = '' 
AS $$
BEGIN
    -- Explicitly update only the column we want
    NEW.updated_at := NOW();
    RETURN NEW;
END;
$$;
CREATE TRIGGER update_base_images_updated_at
    BEFORE UPDATE ON public.base_images
    FOR EACH ROW
    EXECUTE FUNCTION public.update_base_images_updated_at();

-- Enable RLS
ALTER TABLE public.base_images ENABLE ROW LEVEL SECURITY;

-- Revoke access from client roles
REVOKE ALL ON public.base_images FROM anon;
REVOKE ALL ON public.base_images FROM authenticated;

-- Policy: only service_role key can access
CREATE POLICY service_role_access
ON public.base_images
FOR ALL
USING ((SELECT auth.role()) = 'service_role');

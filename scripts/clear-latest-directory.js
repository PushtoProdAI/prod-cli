#!/usr/bin/env node

const { createClient } = require('@supabase/supabase-js');

async function clearLatestDirectory() {
  const supabaseUrl = process.env.SUPABASE_URL;
  const supabaseServiceKey = process.env.SUPABASE_SERVICE_ROLE_KEY;
  
  if (!supabaseUrl || !supabaseServiceKey) {
    console.error('Missing required environment variables: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY');
    process.exit(1);
  }

  const supabase = createClient(supabaseUrl, supabaseServiceKey);

  try {
    console.log('Clearing latest directory...');
    
    // List all files in the latest directory
    console.log('Listing files in latest directory...');
    const { data: files, error: listError } = await supabase.storage
      .from('cli-binaries')
      .list('releases/latest');

    if (listError) {
      console.error('Error listing files:', listError);
      // Continue anyway - directory might not exist yet
      console.log('Directory listing failed, continuing with uploads...');
      return;
    }

    if (!files || files.length === 0) {
      console.log('No files found in latest directory');
      return;
    }

    console.log(`Found ${files.length} files in latest directory:`);
    files.forEach(file => console.log(`  - ${file.name}`));

    // Delete each file
    const deletePromises = files.map(async (file) => {
      console.log(`Deleting file: ${file.name}`);
      const { error: deleteError } = await supabase.storage
        .from('cli-binaries')
        .remove([`releases/latest/${file.name}`]);

      if (deleteError) {
        console.error(`Error deleting ${file.name}:`, deleteError);
        return false;
      } else {
        console.log(`Successfully deleted: ${file.name}`);
        return true;
      }
    });

    const results = await Promise.all(deletePromises);
    const successCount = results.filter(Boolean).length;
    
    console.log(`Deleted ${successCount}/${files.length} files successfully`);

  } catch (error) {
    console.error('Unexpected error:', error);
    process.exit(1);
  }
}

// Run the function
clearLatestDirectory();

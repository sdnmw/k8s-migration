async (page) => {
  const origin = new URL(page.url()).origin;
  const external = [];
  const apiRequests = [];
  await page.context().route('**/*', route => {
    if (new URL(route.request().url()).pathname.startsWith('/api/')) apiRequests.push(route.request().url());
    if (new URL(route.request().url()).origin !== origin) {
      external.push(route.request().url()); return route.abort();
    }
    return route.continue();
  });
  await page.setViewportSize({width:1440,height:900});
  await page.reload();
  await page.getByRole('textbox',{name:'Search'}).fill('Nextcloud');
  await page.getByRole('listbox').waitFor();
  const results = await page.getByRole('option').count();
  if (!results) throw new Error('Local search returned no result');
  await page.getByRole('option',{name:'Kubernetes Nextcloud 完整迁移',exact:true}).click();
  await page.getByRole('heading',{name:'Kubernetes Nextcloud 完整迁移',exact:true}).waitFor();
  await page.reload();
  const image = page.getByRole('img',{name:'图 13：Kubernetes 数据基线'});
  await page.waitForFunction(() => document.querySelector('img[alt="图 13：Kubernetes 数据基线"]')?.classList.contains('medium-zoom-image'));
  await image.click();
  await page.locator('.medium-zoom-image--opened').waitFor();
  await page.keyboard.press('Escape');
  await page.waitForTimeout(500);
  await page.setViewportSize({width:390,height:844});
  await page.goto(origin+'/docs/tutorials/kubernetes-nextcloud/');
  const dimensions = await page.evaluate(()=>({viewport:innerWidth,document:document.documentElement.scrollWidth}));
  if (dimensions.document > dimensions.viewport) throw new Error('Mobile horizontal overflow');
  if (external.length) throw new Error('External runtime resources: '+JSON.stringify(external));
  if (apiRequests.length) throw new Error('Documentation depends on platform API');
  return {localSearchResults:results,zoom:true,deepLinkRefresh:true,mobile:dimensions,externalRuntimeRequests:external.length,platformApiRequests:apiRequests.length};
}

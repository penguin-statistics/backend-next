(function(){var h=document.documentElement,L=h.dataset.copy,D=h.dataset.copied;
if(navigator.clipboard)document.querySelectorAll('[data-c]').forEach(function(e){var b=document.createElement('button');b.type='button';b.className='copy';b.textContent=L;
b.onclick=function(){navigator.clipboard.writeText(e.dataset.c||e.querySelector('code').textContent).then(function(){b.textContent=D;setTimeout(function(){b.textContent=L},1500)})};e.appendChild(b)});
function o(){var t=location.hash&&document.getElementById(location.hash.slice(1));if(t&&t.tagName=='DETAILS')t.open=true}addEventListener('hashchange',o);o()})();

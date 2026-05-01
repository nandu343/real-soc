import { useEffect, useState } from 'react'

export default function JsonDiff({ oldValue, newValue }){
  const [deltaText, setDeltaText] = useState('computing...')

  useEffect(()=>{
    let mounted = true
    ;(async ()=>{
      try{
        const jsondiffpatch = await import('jsondiffpatch')
        const dp = jsondiffpatch.create()
        const delta = dp.diff(oldValue || {}, newValue || {})
        if(!mounted) return
        setDeltaText(delta ? JSON.stringify(delta, null, 2) : 'No differences')
      }catch(err){
        if(!mounted) return
        setDeltaText('diff error: '+String(err))
      }
    })()
    return ()=>{ mounted = false }
  }, [oldValue, newValue])

  const oldPretty = oldValue ? JSON.stringify(oldValue, null, 2) : '{}'
  const newPretty = newValue ? JSON.stringify(newValue, null, 2) : '{}'

  return (
    <div className="grid grid-cols-3 gap-2">
      <div>
        <h4 className="text-sm font-medium">Previous</h4>
        <pre className="mt-1 p-2 bg-white border rounded text-xs font-mono overflow-auto" style={{maxHeight: '360px'}}>{oldPretty}</pre>
      </div>
      <div>
        <h4 className="text-sm font-medium">Delta</h4>
        <pre className="mt-1 p-2 bg-white border rounded text-xs font-mono overflow-auto" style={{maxHeight: '360px'}}>{deltaText}</pre>
      </div>
      <div>
        <h4 className="text-sm font-medium">Current</h4>
        <pre className="mt-1 p-2 bg-white border rounded text-xs font-mono overflow-auto" style={{maxHeight: '360px'}}>{newPretty}</pre>
      </div>
    </div>
  )
}

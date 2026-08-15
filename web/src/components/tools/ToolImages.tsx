import type { ToolActivity } from '@/protocol.gen';

export function ToolImages({ tool }: { tool: ToolActivity }) {
  if (!tool.images?.length) return null;

  return (
    <div className="tool-images">
      {tool.images.map((image, index) => (
        <figure key={`${image.name}-${index}`}>
          <a href={`data:${image.mimeType};base64,${image.data}`} target="_blank" rel="noreferrer">
            <img src={`data:${image.mimeType};base64,${image.data}`} alt={image.name || `Tool result image ${index + 1}`} loading="lazy" />
          </a>
          <figcaption>{image.name || `image-${index + 1}`} · {image.mimeType}</figcaption>
        </figure>
      ))}
    </div>
  );
}

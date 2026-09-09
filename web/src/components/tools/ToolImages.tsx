import type { ToolActivity } from '@/protocol.gen';
import { ImageLightbox } from '@/components/ui/ImageLightbox';

export function ToolImages({ tool }: { tool: ToolActivity }) {
  if (!tool.images?.length) return null;

  return (
    <div className="tool-images">
      {tool.images.map((image, index) => (
        <figure key={`${image.name}-${index}`}>
          <ImageLightbox
            src={`data:${image.mimeType};base64,${image.data}`}
            alt={image.name || `Tool result image ${index + 1}`}
            loading="lazy"
          />
          <figcaption>{image.name || `image-${index + 1}`} · {image.mimeType}</figcaption>
        </figure>
      ))}
    </div>
  );
}
